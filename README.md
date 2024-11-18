# URL Monitoring Application

A Go-based application that monitors multiple URLs and tracks their uptime status. The application provides a web interface to view the status of monitored URLs and sends email notifications when status changes occur.

## Features

- Real-time URL monitoring
- Web interface for status visualization
- Email notifications for status changes
- Tenant-based URL organization
- Historical uptime tracking
- SQL Server database integration for storing timeout records

## Prerequisites

- Go 1.x
- Microsoft SQL Server
- SMTP server access for email notifications

## Configuration

### 1. Database Configuration
Create a `config.json` file with the following structure:
```json
{
  "database": {
    "connectionString": "sqlserver://username:password@server/instance?database=TenantsDB",
    "timeoutInterval": "15s"
  }
}
```

### 2. URL Configuration
Create a `list.json` file with the following structure:
```json
{
  "items": [
    {
      "metadata": {
        "name": "tenant-name",
        "creationTimestamp": "timestamp"
      },
      "network": {
        "externalUrls": [
          "http://example.com/"
        ]
      }
    }
  ]
}
```

## Running the Application

1. Ensure all configuration files are properly set up
2. Run the application:
   ```bash
   go run main.go
   ```
3. Access the web interface at `http://localhost:80`

## Web Interface

- Home page (`/`): Displays status of all monitored URLs
- Tenant page (`/{tenantname}`): Shows detailed history for a specific tenant

## Features

- Color-coded status indicators (green for OK, red for issues)
- Real-time status monitoring
- Consolidated email alerts with status summaries
- Historical timeout tracking
- Clickable tenant links in email notifications

## Notes

- The application monitors URLs by appending "api/FrontEnd/Shell/StaticData" to each external URL
- Email notifications include counts of OK and Not OK statuses
- The web interface automatically refreshes to show current status
