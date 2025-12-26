package governance

/*
Example usage of the governance client in DIAM-GW service:

In your main.go or initialization code:

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat/telco/diam-gw/pkg/governance"
	govmodels "github.com/chronnie/governance/models"
)

func main() {
	// Initialize governance client
	err := governance.Initialize(&governance.Config{
		ManagerURL:       "http://governance-manager:8080", // or from env: os.Getenv("GOVERNANCE_MANAGER_URL")
		ServiceName:      "diam-gw",
		PodName:          os.Getenv("POD_NAME"), // from k8s downward API
		NotificationPort: 9002,                  // port for receiving notifications
		PodIP:            "127.0.0.1",           // or pod IP in k8s
		ServicePort:      3868,                  // your main diameter port
		Subscriptions:    []string{"hss", "eir"}, // services to subscribe to
		Timeout:          10 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to initialize governance: %v", err)
	}

	// Get the governance client
	govClient := governance.MustGetClient()

	// Example 1: Get own pods
	ownPods := govClient.GetOwnPods()
	log.Printf("DIAM-GW service has %d pods", len(ownPods))
	for _, pod := range ownPods {
		log.Printf("  - Pod: %s, Status: %s", pod.PodName, pod.Status)
	}

	// Example 2: Get pods of a subscribed service (e.g., HSS)
	if hssPods, exists := govClient.GetSubscribedServicePods("hss"); exists {
		log.Printf("HSS service has %d pods", len(hssPods))
		for _, pod := range hssPods {
			log.Printf("  - Pod: %s, Status: %s", pod.PodName, pod.Status)
			for _, provider := range pod.Providers {
				log.Printf("    Provider: %s://%s:%d", provider.Protocol, provider.IP, provider.Port)
			}
		}
	}

	// Example 3: Get pods of EIR service
	if eirPods, exists := govClient.GetSubscribedServicePods("eir"); exists {
		log.Printf("EIR service has %d pods", len(eirPods))
		for _, pod := range eirPods {
			log.Printf("  - Pod: %s, Status: %s", pod.PodName, pod.Status)
		}
	}

	// Example 4: Get all subscribed services
	allSubscribed := govClient.GetAllSubscribedServices()
	log.Printf("Subscribed to %d services", len(allSubscribed))
	for serviceName, pods := range allSubscribed {
		log.Printf("  - %s: %d pods", serviceName, len(pods))
	}

	// Example 5: Custom notification handler for routing updates
	govClient.SetNotificationHandler(func(payload *govmodels.NotificationPayload) {
		log.Printf("Custom handler: service=%s, event=%s, pods=%d",
			payload.ServiceName, payload.EventType, len(payload.Pods))

		// Update routing tables based on service changes
		switch payload.ServiceName {
		case "hss":
			updateHssRouting(payload.Pods)
		case "eir":
			updateEirRouting(payload.Pods)
		}
	})

	// ... rest of your application code ...

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown
	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := governance.Shutdown(ctx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}

func updateHssRouting(pods []govmodels.PodInfo) {
	// Example: Update HSS routing table
	for _, pod := range pods {
		if pod.Status == govmodels.StatusHealthy {
			for _, provider := range pod.Providers {
				log.Printf("Adding HSS route: %s:%d", provider.IP, provider.Port)
				// Update your diameter routing table
			}
		}
	}
}

func updateEirRouting(pods []govmodels.PodInfo) {
	// Example: Update EIR routing table
	for _, pod := range pods {
		if pod.Status == govmodels.StatusHealthy {
			for _, provider := range pod.Providers {
				log.Printf("Adding EIR route: %s:%d", provider.IP, provider.Port)
				// Update your diameter routing table
			}
		}
	}
}
```

In Kubernetes deployment:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: diam-gw-pod-1
spec:
  containers:
  - name: diam-gw
    image: diam-gw:latest
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
    ports:
    - containerPort: 3868
      name: diameter
    - containerPort: 9002
      name: governance
```
*/
