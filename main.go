package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/smtp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/gorilla/mux"
)

// Constants for configuration
const (
	defaultPort = 80
	apiEndpoint = "api/FrontEnd/Shell/StaticData"
	configFile  = "config.json"
	tenantsFile = "list.json"
)

// Constants for HTTP status
const (
	statusOK    = 200
	statusError = "Not OK"
	statusGood  = "OK"
)

type TimeoutRecord struct {
	ID         int       `json:"id"`
	TenantName string    `json:"tenantName"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	Duration   int       `json:"duration"` // in seconds
}

// Define a struct to store tenant information
type TenantInfo struct {
	TenantName string
	Status     int
	Message    string
}

func gatherTenantInfo(tenantName string, status int, message string) {
	// Lock to ensure atomic access to tenantInfoMap
	tenantInfoMutex.Lock()
	defer tenantInfoMutex.Unlock()

	// Store tenant information in the map
	tenantInfoMap[tenantName] = TenantInfo{
		TenantName: tenantName,
		Status:     status,
		Message:    message,
	}
}

var tenantInfoMap = make(map[string]TenantInfo)
var tenantInfoMutex sync.Mutex

func sendConsolidatedEmail() {
	// Lock to ensure atomic access to tenantInfoMap
	tenantInfoMutex.Lock()
	defer tenantInfoMutex.Unlock()

	// Check if there is any information in the map
	if len(tenantInfoMap) == 0 {
		// No information to include, skip sending the email
		return
	}

	// Count OK and NOT OK statuses
	okCount, notOkCount := 0, 0
	for _, tenantInfo := range tenantInfoMap {
		if tenantInfo.Status == 200 {
			okCount++
		} else {
			notOkCount++
		}
	}

	// Prepare the email subject with status counts
	emailSubject := fmt.Sprintf("[Alert]Status Change-OK: %d, Not Ok: %d", okCount, notOkCount)

	// Prepare the email body with information from all tenants
	var emailBody strings.Builder
	emailBody.WriteString("<h2 style=\"color: #333;\">Consolidated Tenant Information:</h2>\n\n")
	emailBody.WriteString("<table style=\"border-collapse: collapse; width: 100%; margin-top: 15px;\">\n")
	emailBody.WriteString("<tr style=\"background-color: #f2f2f2;\">\n")
	emailBody.WriteString("<th style=\"padding: 12px; text-align: left;\">TenantLog</th>\n")
	emailBody.WriteString("<th style=\"padding: 12px; text-align: left;\">Status</th>\n")
	emailBody.WriteString("<th style=\"padding: 12px; text-align: left;\">Message</th>\n")
	emailBody.WriteString("</tr>\n")

	// Sort tenantInfoMap by Status, Not OK entries first
	var sortedTenants []TenantInfo
	for _, tenantInfo := range tenantInfoMap {
		sortedTenants = append(sortedTenants, tenantInfo)
	}
	sort.Slice(sortedTenants, func(i, j int) bool {
		return sortedTenants[i].Status != 200 && sortedTenants[j].Status == 200
	})

	// Display tenant information in the table
	for _, tenantInfo := range sortedTenants {
		statusColor := "green" // Assume OK status
		if tenantInfo.Status != 200 {
			statusColor = "red" // Set color to red for Not OK status
		}

		emailBody.WriteString("<tr>\n")
		// Make the tenant name clickable with a link
		tenantHistory := fmt.Sprintf("http://172.20.104.49/%s", tenantInfo.TenantName)
		emailBody.WriteString(fmt.Sprintf("<td style=\"padding: 12px;\"><a href=\"%s\" target=\"_blank\">%s</a></td>\n", tenantHistory, tenantInfo.TenantName))
		emailBody.WriteString(fmt.Sprintf("<td style=\"padding: 12px; color: %s;\">%s</td>\n", statusColor, getStatusText(tenantInfo.Status)))
		emailBody.WriteString(fmt.Sprintf("<td style=\"padding: 12px;\">%s</td>\n", tenantInfo.Message))
		emailBody.WriteString("</tr>\n")
	}

	emailBody.WriteString("</table>")

	// Clear the map after gathering information
	tenantInfoMap = make(map[string]TenantInfo)

	// Send the consolidated email with the subject
	sendEmail(emailSubject, emailBody.String())
}
func getStatusText(status int) string {
	if status == 200 {
		return "OK"
	}
	return "Not OK"
}

// Site represents a monitored website.
type Site struct {
	URL        string
	Status     int
	PrevStatus int
	Mutex      sync.Mutex
}
type Items struct {
	Tenants []TenantItem `json:"items"`
}

// Tenant represents a tenant from the list.json file.
type TenantMetadata struct {
	Name              string `json:"name"`
	CreationTimestamp string `json:"creationTimestamp"`
}
type TenantNetwork struct {
	ExternalUrls []string `json:"externalUrls"`
}
type TenantItem struct {
	Metadata TenantMetadata `json:"metadata"`
	Network  TenantNetwork  `json:"network"`
	// Add other fields as needed
}
type DurationJSON struct {
	time.Duration
}
type Config struct {
	Database struct {
		ConnectionString string       `json:"connectionString"`
		TimeoutInterval  DurationJSON `json:"timeoutInterval"`
	} `json:"database"`
}

var sites = map[string]*Site{}
var mu sync.Mutex
var db *sql.DB

func main() {
	initDB()
	// Load data from list.json
	tenants, err := loadTenants(tenantsFile)
	if err != nil {
		log.Fatalf("Error loading tenants: %v", err)
	}

	r := mux.NewRouter()

	r.HandleFunc("/", homeHandler).Methods("GET")
	r.HandleFunc("/{tenantname}", tenantHandler).Methods("GET")

	// Start the monitoring routine
	go monitorSites()

	// Populate the sites map with URLs from list.json
	mu.Lock()
	for _, tenant := range tenants {
		for _, externalURL := range tenant.Network.ExternalUrls {
			url := externalURL + apiEndpoint
			site := &Site{URL: url}
			sites[url] = site
		}
	}
	mu.Unlock()

	// Start the HTTP server
	fmt.Printf("Server is running on http://localhost:%d\n", defaultPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", defaultPort), r))
}
func initDB() {
	configFile, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var config Config
	if err := json.Unmarshal(configFile, &config); err != nil {
		log.Fatalf("Error unmarshalling config file: %v", err)
	}

	// Open the database connection
	var errDB error
	db, errDB = sql.Open("sqlserver", config.Database.ConnectionString)
	if errDB != nil {
		log.Fatal(errDB)
	}

	// Create the TimeoutRecords table if it doesn't exist
	_, errDB = db.Exec(`
	IF NOT EXISTS (SELECT * FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = 'TimeoutRecords')
	BEGIN
		CREATE TABLE TimeoutRecords (
			id INT PRIMARY KEY IDENTITY,
			tenantName NVARCHAR(255),
			startTime DATETIME,
			endTime DATETIME,
			duration INT
		);
	END;
	`)
	if errDB != nil {
		log.Fatal(errDB)
	}
}
func (d *DurationJSON) UnmarshalJSON(b []byte) error {
	s := string(b)
	// Remove surrounding quotes if present
	if len(s) > 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = duration
	return nil
}
func loadTenants(filePath string) ([]TenantItem, error) {
	file, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var items Items
	err = json.Unmarshal(file, &items)
	if err != nil {
		return nil, err
	}

	return items.Tenants, nil
}
func tenantHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	// Extract the tenant name from the URL
	vars := mux.Vars(r)
	tenantName := vars["tenantname"]

	// Set the content type to HTML
	w.Header().Set("Content-Type", "text/html")

	// Display the table header
	fmt.Fprintf(w, "<html><head><title>Timeout Records for %s</title>", tenantName)
	fmt.Fprintf(w, `<style>
        body {
            background-color: #333; /* Dark gray background */
            color: #fff; /* White text */
            font-family: Arial, sans-serif;
        }
        table {
            border-collapse: collapse;
            width: 100%; 
        }
        th, td {
            border: 1px solid #ddd;
            padding: 10px;
            text-align: left;
        }
        th {
            background-color: #555; /* Dark gray header background */
        }
    </style></head><body>`)

	// Display the table for the specified tenant
	fmt.Fprintf(w, "<h2>Timeout Records for %s</h2>", tenantName)
	fmt.Fprintf(w, "<table><tr><th>Start Time</th><th>End Time</th><th>Duration</th></tr>")

	// Query the database for timeout records for the specified tenant
	rows, err := db.Query(`
        SELECT startTime, endTime, duration
        FROM dbo.TimeoutRecords
        WHERE tenantName = @p1
    `, sql.Named("p1", tenantName))
	if err != nil {
		log.Println("Error querying timeout records:", err)
		fmt.Fprintf(w, "</table></body></html>")
		return
	}
	defer rows.Close()

	// Display each row in the table
	for rows.Next() {
		var startTime, endTime time.Time
		var duration int
		if err := rows.Scan(&startTime, &endTime, &duration); err != nil {
			log.Println("Error scanning row:", err)
			continue
		}
		fmt.Fprintf(w, "<tr><td>%s</td><td>%s</td><td>%d seconds</td></tr>", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"), duration)
	}

	fmt.Fprintf(w, "</table></body></html>")
}
func homeHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	// Set the content type to HTML
	w.Header().Set("Content-Type", "text/html")

	// Display the summary table
	fmt.Fprintf(w, "<html><head><title>Monitored Sites</title>")
	fmt.Fprintf(w, `<style>
        body {
            background-color: #333; /* Dark gray background */
            color: #fff; /* White text */
            font-family: Arial, sans-serif;
        }
        table {
            border-collapse: collapse;
            width: 100%; 
        }
        th, td {
            border: 1px solid #ddd;
            padding: 10px;
            text-align: left;
        }
        th {
            background-color: #555; /* Dark gray header background */
        }
        a {
            color: #007bff;
            text-decoration: none;
        }
    </style>
    <script>
        // Autosize columns on table load
        window.onload = function() {
            autosizeColumns();
        };

        // Refresh UI data every 10 seconds
        setInterval(function() {
            location.reload();
        }, 10000);

        // Autosize columns function
        function autosizeColumns() {
            var table = document.getElementById("summaryTable");
            for (var i = 0; i < table.rows[0].cells.length; i++) {
                table.rows[0].cells[i].style.width = "auto";
            }

            table = document.getElementById("monitorTable");
            for (var i = 0; i < table.rows[0].cells.length; i++) {
                table.rows[0].cells[i].style.width = "auto";
            }
        }
    </script>`)

	// Summary table
	fmt.Fprintf(w, "<h2>Tenants Summary</h2>")
	fmt.Fprintf(w, "<table id=\"summaryTable\"><tr><th>Total Tenants</th><th>Healthy Tenants</th><th>Unhealthy Tenants</th></tr>")
	totalTenants := len(sites)
	healthyTenants := 0
	for _, site := range sites {
		if site.Status == http.StatusOK {
			healthyTenants++
		}
	}
	unhealthyTenants := totalTenants - healthyTenants
	fmt.Fprintf(w, "<tr><td>%d</td><td>%d</td><td>%d</td></tr></table>", totalTenants, healthyTenants, unhealthyTenants)

	// Detailed table
	fmt.Fprintf(w, "<h2>Detailed Monitoring</h2>")
	fmt.Fprintf(w, "<table id=\"monitorTable\"><tr><th>Tenant</th><th>URL</th><th>Status</th></tr>")

	// Create a slice for sorted sites
	var sortedSites []*Site
	for _, site := range sites {
		sortedSites = append(sortedSites, site)
	}

	// Sort the slice based on status (other than 200 first)
	sort.Slice(sortedSites, func(i, j int) bool {
		return sortedSites[i].Status != http.StatusOK && sortedSites[j].Status == http.StatusOK
	})

	// Display rows in the original order
	for _, site := range sortedSites {
		// Extract tenant name from the URL
		tenantName := extractTenantName(site.URL)

		// Determine the color based on the status code
		statusColor := getStatusColor(site.Status)

		// Display the row with tenant name, URL (as a link), and status
		fmt.Fprintf(w, "<tr><td><a href=\"%s\">%s</a></td><td><a href=\"%s\" target=\"_blank\">%s</a></td><td style=\"color:%s;\">%d</td></tr>", tenantName, tenantName, site.URL, site.URL, statusColor, site.Status)
	}

	fmt.Fprintf(w, "</table></body></html>")
}
func getStatusColor(status int) string {
	if status == http.StatusOK {
		return "green" // Green color for OK (200)
	}
	return "red" // Red color for other status codes
}

// extractTenantName extracts the tenant name from the URL
func extractTenantName(url string) string {
	// Split the URL by "//" and take the second part
	parts := strings.Split(url, "//")
	if len(parts) >= 2 {
		// Split the second part by "." and take the first part
		tenantParts := strings.Split(parts[1], ".")
		if len(tenantParts) >= 1 {
			return tenantParts[0]
		}
	}
	return "Unknown"
}
func monitorSites() {
	for {
		var wg sync.WaitGroup

		mu.Lock()
		for _, site := range sites {
			wg.Add(1)
			go func(s *Site) {
				defer wg.Done()
				checkSite(s)
			}(site)
		}
		mu.Unlock()

		// Wait for all goroutines to finish before sleeping
		wg.Wait()
		sendConsolidatedEmail()
		// Check sites every 10 seconds
		time.Sleep(60 * time.Second)
	}
}
func insertTimeoutRecord(tenantURL string) {
	// Extract tenant name from the URL
	tenantName := extractTenantName(tenantURL)

	_, err := db.Exec(`
		INSERT INTO dbo.TimeoutRecords (tenantName, startTime)
		VALUES (@p1, GETDATE())
	`, sql.Named("p1", tenantName))
	if err != nil {
		log.Println("Error inserting timeout record:", err)
		return
	}

	message := "Tenant is currently unreachable, and a timeout record has been inserted."
	gatherTenantInfo(tenantName, 500, message) // Set status to 500 for insertion
}
func updateTimeoutRecord(tenantURL string) error {
	// Extract tenant name from the URL
	tenantName := extractTenantName(tenantURL)

	// Check if a record with the specified conditions exists
	var recordExists bool
	err := db.QueryRow(`
	  SELECT CASE WHEN EXISTS (
		SELECT 1
		FROM dbo.TimeoutRecords
		WHERE tenantName = @p1 AND endTime IS NULL
	  ) THEN 1 ELSE 0 END
	`, sql.Named("p1", tenantName)).Scan(&recordExists)

	if err != nil {
		log.Println("Error checking if record exists:", err)
		return err
	}

	// If the record exists, proceed with the update
	if recordExists {
		_, err := db.Exec(`
		  UPDATE dbo.TimeoutRecords
		  SET endTime = GETDATE(),
			  duration = CASE
				  WHEN DATEDIFF(SECOND, startTime, GETDATE()) < 1 THEN 1
				  ELSE DATEDIFF(SECOND, startTime, GETDATE())
			  END
		  WHERE tenantName = @p1 AND endTime IS NULL
	  `, sql.Named("p1", tenantName))

		if err != nil {
			log.Println("Error updating timeout record:", err)
			return err
		}

		message := "Tenant has recovered, and the timeout record has been updated"
		gatherTenantInfo(tenantName, 200, message) // Set status to 200 for update
	}

	return nil
}
func sendEmail(subject, body string) {
	smtpServer := "smtp.gmail.com"
	smtpPort := 587
	username := "TESTg@gmail.com"
	password := "APP-PASSWORD"
	senderAddress := "TEST@gmail.com"

	// Hardcoded recipient addresses
	recipientAddresses := []string{"YourEmailAddresses"}

	mime := "MIME-version: 1.0;\r\nContent-Type: text/html; charset=\"UTF-8\";\r\n\r\n"
	subjectHeader := fmt.Sprintf("Subject: %s\r\n", subject)
	toHeader := fmt.Sprintf("To: %s\r\n", strings.Join(recipientAddresses, ", "))

	auth := smtp.PlainAuth("", username, password, smtpServer)

	for _, recipientAddress := range recipientAddresses {
		// Explicitly set "To" in the message header
		msg := []byte(toHeader + subjectHeader + mime + body)

		err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpServer, smtpPort), auth, senderAddress, []string{recipientAddress}, msg)
		if err != nil {
			log.Printf("Error sending email to %s: %v", recipientAddress, err)
		}
	}
}

func checkSite(site *Site) {
	client := &http.Client{
		Timeout: 30 * time.Second, // Set a timeout of 30 seconds (or adjust as needed)
	}
	resp, err := client.Get(site.URL)
	if err != nil {
		// Handle error (e.g., log it)
		log.Println("Error checking site:", err)
		return
	}

	// Lock to ensure atomic access to Status and PrevStatus
	site.Mutex.Lock()
	defer site.Mutex.Unlock()

	// Store the current status as PrevStatus
	site.PrevStatus = site.Status

	// Update the Status with the new status
	site.Status = resp.StatusCode

	// Check conditions to insert or update timeout record
	if site.Status != http.StatusOK && site.PrevStatus == http.StatusOK {
		// Status changed from OK to non-OK, insert timeout record
		insertTimeoutRecord(site.URL)
	} else if site.Status == http.StatusOK && site.PrevStatus != http.StatusOK {
		// Status changed from non-OK to OK, update timeout record
		updateTimeoutRecord(site.URL)
	}
}
