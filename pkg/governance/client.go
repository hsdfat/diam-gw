package governance

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	govclient "github.com/chronnie/governance/client"
	"github.com/chronnie/governance/models"
	diamgwconfig "github.com/hsdfat/diam-gw/pkg/config"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

var (
	// globalClient is the singleton governance client instance
	globalClient     *Client
	globalClientOnce sync.Once
	globalClientMu   sync.RWMutex
)

// Client wraps the governance client with service-specific configuration
type Client struct {
	govClient      *govclient.Client
	serviceName    string
	podName        string
	subscriptions  []string
	notifHandler   govclient.NotificationHandler
	isRegistered   bool
	mu             sync.RWMutex
}

// Config holds the governance client configuration
type Config struct {
	// Manager URL (e.g., "http://localhost:8080")
	ManagerURL string

	// Service name (e.g., "diam-gw")
	ServiceName string

	// Pod name (e.g., from hostname or env var)
	PodName string

	// HTTP server port for notifications and health checks
	NotificationPort int

	// Pod IP address (use 127.0.0.1 for local, or actual pod IP in k8s)
	PodIP string

	// HTTP port for the main service
	ServicePort int

	// Services to subscribe to (e.g., ["hss", "eir"])
	Subscriptions []string

	// Timeout for governance API calls
	Timeout time.Duration
}

// InitializeFromDiamGWConfig initializes the global governance client from DIAM-GW config
// This is the preferred way to initialize for DIAM-GW service
// Panics if governance config validation fails
func InitializeFromDiamGWConfig(diamgwCfg *diamgwconfig.Config) error {
	// Validate governance configuration
	if err := diamgwCfg.Governance.Validate(); err != nil {
		panic(fmt.Sprintf("Invalid governance configuration: %v", err))
	}

	if !diamgwCfg.Governance.Enabled {
		log.Println("[Governance] Governance client is disabled in configuration")
		return nil
	}

	// Extract port from ListenAddr (format: "0.0.0.0:3868")
	listenParts := strings.Split(diamgwCfg.Server.ListenAddr, ":")
	servicePort := 3868 // default
	if len(listenParts) == 2 {
		if port, err := strconv.Atoi(listenParts[1]); err == nil {
			servicePort = port
		}
	}

	return Initialize(&Config{
		ManagerURL:       diamgwCfg.Governance.ManagerURL,
		ServiceName:      diamgwCfg.Governance.ServiceName,
		PodName:          diamgwCfg.Governance.PodName,
		NotificationPort: diamgwCfg.Governance.NotificationPort,
		PodIP:            diamgwCfg.Governance.PodIP,
		ServicePort:      servicePort,
		Subscriptions:    diamgwCfg.Governance.Subscriptions,
		Timeout:          diamgwCfg.Governance.Timeout,
	})
}

// Initialize initializes the global governance client
// This should be called once during application startup
func Initialize(cfg *Config) error {
	var initErr error
	globalClientOnce.Do(func() {
		if cfg.Timeout == 0 {
			cfg.Timeout = 10 * time.Second
		}

		if cfg.PodName == "" {
			// Try to get from hostname
			hostname, err := os.Hostname()
			if err != nil {
				cfg.PodName = fmt.Sprintf("%s-pod-unknown", cfg.ServiceName)
			} else {
				cfg.PodName = hostname
			}
		}

		client := &Client{
			serviceName:   cfg.ServiceName,
			podName:       cfg.PodName,
			subscriptions: cfg.Subscriptions,
		}

		// Create governance client with logger
		client.govClient = govclient.NewClient(&govclient.ClientConfig{
			ManagerURL:  cfg.ManagerURL,
			ServiceName: cfg.ServiceName,
			PodName:     cfg.PodName,
			Timeout:     cfg.Timeout,
			Logger:      logger.WithFields("component", "governance"),
		})

		// Create default notification handler
		client.notifHandler = func(payload *models.NotificationPayload) {
			log.Printf("[Governance] Received notification: service=%s, event=%s, pods=%d",
				payload.ServiceName, payload.EventType, len(payload.Pods))

			for _, pod := range payload.Pods {
				log.Printf("[Governance]   - Pod: %s, Status: %s, Providers: %d",
					pod.PodName, pod.Status, len(pod.Providers))
			}
		}

		// Register with governance manager
		// Note: Health check and notification URLs should be set up separately
		// using the HTTP server methods
		registration := &models.ServiceRegistration{
			ServiceName: cfg.ServiceName,
			PodName:     cfg.PodName,
			Providers: []models.ProviderInfo{
				{
					Protocol: models.ProtocolHTTP,
					IP:       cfg.PodIP,
					Port:     cfg.ServicePort,
				},
			},
			HealthCheckURL:  fmt.Sprintf("http://%s:%d/health", cfg.PodIP, cfg.NotificationPort),
			NotificationURL: fmt.Sprintf("http://%s:%d/notify", cfg.PodIP, cfg.NotificationPort),
			Subscriptions:   nil,
		}

		resp, err := client.govClient.Register(registration)
		if err != nil {
			initErr = fmt.Errorf("failed to register with governance: %w", err)
			return
		}

		client.mu.Lock()
		client.isRegistered = true
		client.mu.Unlock()

		log.Printf("[Governance] Service registered successfully!")
		log.Printf("[Governance]   - Service: %s", cfg.ServiceName)
		log.Printf("[Governance]   - Pod: %s", cfg.PodName)
		log.Printf("[Governance]   - Own pods: %d", len(resp.Pods))
		log.Printf("[Governance]   - Subscribed services: %d", len(resp.SubscribedServices))

		for svcName, pods := range resp.SubscribedServices {
			log.Printf("[Governance]     * %s: %d pods", svcName, len(pods))
		}

		globalClientMu.Lock()
		globalClient = client
		globalClientMu.Unlock()
	})

	return initErr
}

// GetClient returns the global governance client instance
// Returns nil if not initialized
func GetClient() *Client {
	globalClientMu.RLock()
	defer globalClientMu.RUnlock()
	return globalClient
}

// MustGetClient returns the global governance client instance
// Panics if not initialized
func MustGetClient() *Client {
	client := GetClient()
	if client == nil {
		panic("governance client not initialized, call Initialize() first")
	}
	return client
}

// Register registers the service with the governance manager
// This is called automatically during Initialize()
func (c *Client) Register() error {
	return fmt.Errorf("use Initialize() to register, manual registration not supported")
}

// Unregister removes this service from the governance manager
func (c *Client) Unregister() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRegistered {
		return fmt.Errorf("service not registered")
	}

	err := c.govClient.Unregister()
	if err != nil {
		return fmt.Errorf("failed to unregister: %w", err)
	}

	c.isRegistered = false
	log.Printf("[Governance] Service unregistered: %s:%s", c.serviceName, c.podName)

	return nil
}

// GetOwnPods returns the list of pods for this service
func (c *Client) GetOwnPods() []models.PodInfo {
	return c.govClient.GetOwnPods()
}

// GetSubscribedServicePods returns the pods for a specific subscribed service
func (c *Client) GetSubscribedServicePods(serviceName string) ([]models.PodInfo, bool) {
	return c.govClient.GetSubscribedServicePods(serviceName)
}

// GetAllSubscribedServices returns all subscribed services and their pods
func (c *Client) GetAllSubscribedServices() map[string][]models.PodInfo {
	return c.govClient.GetAllSubscribedServices()
}

// SetNotificationHandler sets a custom notification handler
// This will replace the default handler
func (c *Client) SetNotificationHandler(handler govclient.NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifHandler = handler
}

// Shutdown gracefully shuts down the governance client
func (c *Client) Shutdown(ctx context.Context) error {
	log.Println("[Governance] Shutting down...")

	// Unregister from governance
	if err := c.Unregister(); err != nil {
		log.Printf("[Governance] Error during unregister: %v", err)
	}

	log.Println("[Governance] Shutdown complete")
	return nil
}

// IsRegistered returns whether the service is currently registered
func (c *Client) IsRegistered() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isRegistered
}

// GetServiceName returns the service name
func (c *Client) GetServiceName() string {
	return c.serviceName
}

// GetPodName returns the pod name
func (c *Client) GetPodName() string {
	return c.podName
}

// GetSubscriptions returns the list of subscribed services
func (c *Client) GetSubscriptions() []string {
	return c.subscriptions
}

// StartHTTPServer starts an HTTP server with all governance endpoints including subscribed services
// This is a convenience method that includes notification, heartbeat, health check, and subscribed services endpoints
func (c *Client) StartHTTPServer(config govclient.HTTPServerConfig) error {
	return c.govClient.StartHTTPServerWithClient(config)
}

// CreateSubscribedServicesHandler creates an HTTP handler for the subscribed services endpoint
// This can be integrated into existing HTTP servers (gin, chi, stdlib, etc.)
func (c *Client) CreateSubscribedServicesHandler() http.HandlerFunc {
	return c.govClient.CreateSubscribedServicesHandler()
}

// Shutdown shuts down the global governance client
func Shutdown(ctx context.Context) error {
	client := GetClient()
	if client == nil {
		return nil
	}
	return client.Shutdown(ctx)
}
