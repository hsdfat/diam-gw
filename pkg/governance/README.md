# Governance Client for DIAM-GW Service

This package provides a global governance client for the DIAM-GW service to register with the governance manager and receive notifications about other services.

## Features

- **Global singleton client** - Single instance accessible throughout the application
- **Automatic registration** - Registers with governance manager on initialization
- **Pod discovery** - Get real-time information about own pods and subscribed services
- **Notification handling** - Receive updates when services register/unregister
- **Thread-safe** - Safe for concurrent access
- **Graceful shutdown** - Clean unregistration and resource cleanup

## Installation

The governance client is already included in the DIAM-GW service codebase at `pkg/governance`.

## Quick Start

### 1. Initialize during application startup

```go
import "github.com/hsdfat/telco/diam-gw/pkg/governance"

func main() {
    // Initialize governance client
    err := governance.Initialize(&governance.Config{
        ManagerURL:       "http://governance-manager:8080",
        ServiceName:      "diam-gw",
        PodName:          os.Getenv("POD_NAME"),
        NotificationPort: 9002,
        PodIP:            os.Getenv("POD_IP"),
        ServicePort:      3868,
        Subscriptions:    []string{"hss", "eir"},
    })
    if err != nil {
        log.Fatalf("Failed to initialize governance: %v", err)
    }

    // ... rest of your app ...
}
```

### 2. Access the client anywhere in your code

```go
// Get the global client
govClient := governance.MustGetClient()

// Get own pods
ownPods := govClient.GetOwnPods()
log.Printf("DIAM-GW has %d pods", len(ownPods))

// Get subscribed service pods
if hssPods, exists := govClient.GetSubscribedServicePods("hss"); exists {
    for _, pod := range hssPods {
        log.Printf("HSS Pod: %s [%s]", pod.PodName, pod.Status)
    }
}

if eirPods, exists := govClient.GetSubscribedServicePods("eir"); exists {
    for _, pod := range eirPods {
        log.Printf("EIR Pod: %s [%s]", pod.PodName, pod.Status)
    }
}
```

### 3. Shutdown gracefully

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := governance.Shutdown(ctx); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

## API Reference

### Initialization

#### `Initialize(cfg *Config) error`

Initializes the global governance client. Should be called once during application startup.

**Config fields:**
- `ManagerURL` - URL of the governance manager (e.g., "http://governance-manager:8080")
- `ServiceName` - Name of this service (e.g., "diam-gw")
- `PodName` - Unique pod identifier (usually from k8s downward API)
- `NotificationPort` - Port for receiving notifications and health checks
- `PodIP` - IP address of this pod
- `ServicePort` - Main service port (diameter port)
- `Subscriptions` - List of services to subscribe to
- `Timeout` - Timeout for API calls (default: 10s)

### Client Methods

#### `GetClient() *Client`

Returns the global client instance. Returns nil if not initialized.

#### `MustGetClient() *Client`

Returns the global client instance. Panics if not initialized.

#### `GetOwnPods() []models.PodInfo`

Returns the list of pods for this service.

#### `GetSubscribedServicePods(serviceName string) ([]models.PodInfo, bool)`

Returns the pods for a specific subscribed service.

**Returns:**
- `[]models.PodInfo` - List of pods
- `bool` - true if the service exists in subscriptions

#### `GetAllSubscribedServices() map[string][]models.PodInfo`

Returns all subscribed services and their pods.

#### `SetNotificationHandler(handler NotificationHandler)`

Sets a custom notification handler. Replaces the default handler.

```go
govClient.SetNotificationHandler(func(payload *models.NotificationPayload) {
    log.Printf("Service %s changed: %s", payload.ServiceName, payload.EventType)

    // Update diameter routing based on service changes
    if payload.ServiceName == "hss" {
        updateHssRouting(payload.Pods)
    }
})
```

#### `Shutdown(ctx context.Context) error`

Gracefully shuts down the client, unregisters from governance, and stops the notification server.

### Helper Methods

#### `IsRegistered() bool`

Returns whether the service is currently registered.

#### `GetServiceName() string`

Returns the service name.

#### `GetPodName() string`

Returns the pod name.

#### `GetSubscriptions() []string`

Returns the list of subscribed services.

## Data Structures

### PodInfo

```go
type PodInfo struct {
    PodName   string         // Pod identifier
    Status    ServiceStatus  // healthy, unhealthy, unknown
    Providers []ProviderInfo // List of service endpoints
}
```

### ProviderInfo

```go
type ProviderInfo struct {
    Protocol Protocol // http, grpc, diameter
    IP       string   // IP address
    Port     int      // Port number
}
```

## Use Cases for DIAM-GW

### 1. Dynamic Routing Updates

Update diameter routing table when HSS or EIR instances change:

```go
govClient.SetNotificationHandler(func(payload *models.NotificationPayload) {
    switch payload.ServiceName {
    case "hss":
        updateHssRoutes(payload.Pods)
    case "eir":
        updateEirRoutes(payload.Pods)
    }
})

func updateHssRoutes(pods []models.PodInfo) {
    for _, pod := range pods {
        if pod.Status == models.StatusHealthy {
            // Add to routing table
        }
    }
}
```

### 2. Load Balancing

Get all healthy HSS pods for load balancing:

```go
func getHealthyHssPods() []models.PodInfo {
    pods, exists := governance.MustGetClient().GetSubscribedServicePods("hss")
    if !exists {
        return nil
    }

    healthy := []models.PodInfo{}
    for _, pod := range pods {
        if pod.Status == models.StatusHealthy {
            healthy = append(healthy, pod)
        }
    }
    return healthy
}
```

### 3. Connection Pool Management

Maintain diameter connections to backend services:

```go
func maintainConnections() {
    allServices := governance.MustGetClient().GetAllSubscribedServices()

    for serviceName, pods := range allServices {
        for _, pod := range pods {
            if pod.Status == models.StatusHealthy {
                for _, provider := range pod.Providers {
                    ensureConnection(serviceName, provider.IP, provider.Port)
                }
            }
        }
    }
}
```

## Environment Variables

Recommended environment variables for Kubernetes:

```yaml
env:
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: POD_IP
  valueFrom:
    fieldRef:
      fieldPath: status.podIP
- name: GOVERNANCE_MANAGER_URL
  value: "http://governance-manager:8080"
```

## Example Integration

See [example.go](./example.go) for a complete example of integrating the governance client into the DIAM-GW service.

## Thread Safety

All methods are thread-safe and can be called concurrently from multiple goroutines.

## Error Handling

The client handles transient errors gracefully:
- Network failures during registration are returned as errors
- Notification delivery failures are logged but don't crash the application
- The client maintains pod info even if notifications are temporarily unavailable
