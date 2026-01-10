package stats

import (
	"fmt"
	"time"

	"github.com/hsdfat/diam-gw/gateway"
	unifiedStats "github.com/hsdfat/telco/stats"
)

// ConvertToUnifiedStats converts Diam-GW GatewayStatsSnapshot to unified stats model
func ConvertToUnifiedStats(gwStats *gateway.GatewayStatsSnapshot, startTime time.Time) *unifiedStats.ServiceStats {
	uptime := time.Since(startTime).String()

	stats := &unifiedStats.ServiceStats{
		ServiceName:    "Diam-GW",
		ServiceVersion: "1.0.0",
		Uptime:         uptime,
		Timestamp:      time.Now(),
		Connections: unifiedStats.ConnectionStats{
			Total:  gwStats.InServer.TotalConnections,
			Active: gwStats.InServer.ActiveConnections,
		},
		Requests: unifiedStats.RequestStats{
			Total:       gwStats.TotalRequests,
			Success:     gwStats.TotalResponses,
			Failed:      gwStats.TotalErrors,
			BytesSent:   gwStats.InServer.TotalBytesSent,
			BytesRecv:   gwStats.InServer.TotalBytesReceived,
			BySource:    make(map[string]unifiedStats.SourceStats),
			ByOperation: make(map[string]unifiedStats.OperationStats),
		},
		Performance: unifiedStats.PerformanceStats{
			RequestsPerSecond: calculateTPS(gwStats, startTime),
			AvgLatencyMs:      gwStats.AverageLatencyMs,
		},
		Errors: unifiedStats.ErrorStats{
			Total:       gwStats.TotalErrors,
			ByInterface: make(map[string]uint64),
			ByType:      make(map[string]uint64),
		},
		InterfaceStats: make(map[string]interface{}),
		CustomMetrics:  make(map[string]interface{}),
	}

	// Add error breakdown by type
	if gwStats.TimeoutErrors > 0 {
		stats.Errors.ByType["timeout"] = gwStats.TimeoutErrors
	}
	if gwStats.RoutingErrors > 0 {
		stats.Errors.ByType["routing"] = gwStats.RoutingErrors
	}

	// Convert Diameter-specific stats
	diamStats := &unifiedStats.DiameterStats{
		Applications: make(map[int]unifiedStats.ApplicationStats),
	}

	// Process interface stats from InServer
	for appID, ifStats := range gwStats.InServer.InterfaceStats {
		appStats := unifiedStats.ApplicationStats{
			ApplicationID: appID,
			Name:          getApplicationName(appID),
			MessagesSent:  ifStats.MessagesSent,
			MessagesRecv:  ifStats.MessagesReceived,
			BytesSent:     ifStats.BytesSent,
			BytesRecv:     ifStats.BytesReceived,
			Errors:        ifStats.Errors,
			Commands:      make(map[int]unifiedStats.CommandStats),
		}

		// Convert command stats
		for cmdCode, cmdStats := range ifStats.CommandStats {
			appStats.Commands[cmdCode] = unifiedStats.CommandStats{
				CommandCode:  cmdCode,
				Name:         getCommandName(cmdCode),
				RequestsSent: cmdStats.MessagesSent,
				RequestsRecv: cmdStats.MessagesReceived,
				Errors:       cmdStats.Errors,
			}
		}

		diamStats.Applications[appID] = appStats

		// Track by interface for unified format
		appName := getApplicationName(appID)
		totalMessages := ifStats.MessagesReceived + ifStats.MessagesSent
		stats.Requests.BySource[appName] = unifiedStats.SourceStats{
			Total:   totalMessages,
			Success: totalMessages - ifStats.Errors,
			Failed:  ifStats.Errors,
		}

		// Track errors by interface
		if ifStats.Errors > 0 {
			stats.Errors.ByInterface[appName] = ifStats.Errors
		}
	}

	// Add gateway-specific metrics including session stats
	gatewayMetrics := map[string]interface{}{
		"total_forwarded":    gwStats.TotalForwarded,
		"total_from_dra":     gwStats.TotalFromDRA,
		"dra_pool":           gwStats.DraPool,
		"sessions": map[string]uint64{
			"active":    gwStats.ActiveSessions,
			"created":   gwStats.SessionsCreated,
			"completed": gwStats.SessionsCompleted,
			"expired":   gwStats.SessionsExpired,
		},
	}
	stats.CustomMetrics["gateway"] = gatewayMetrics
	stats.CustomMetrics["diameter"] = diamStats

	return stats
}

func calculateTPS(gwStats *gateway.GatewayStatsSnapshot, startTime time.Time) float64 {
	duration := time.Since(startTime).Seconds()
	if duration == 0 {
		return 0
	}
	return float64(gwStats.TotalRequests) / duration
}

func getApplicationName(appID int) string {
	switch appID {
	case 0:
		return "base"
	case 16777251:
		return "s6a"
	case 16777252:
		return "s13"
	case 16777216:
		return "cx"
	case 16777217:
		return "sh"
	default:
		return fmt.Sprintf("app_%d", appID)
	}
}

func getCommandName(cmdCode int) string {
	switch cmdCode {
	case 257:
		return "CER/CEA"
	case 280:
		return "DWR/DWA"
	case 282:
		return "DPR/DPA"
	case 316:
		return "ULR/ULA"
	case 317:
		return "CLR/CLA"
	case 318:
		return "AIR/AIA"
	case 319:
		return "IDR/IDA"
	case 321:
		return "PUR/PUA"
	case 323:
		return "NOR/NOA"
	case 324:
		return "MICR/MICA"
	default:
		return fmt.Sprintf("cmd_%d", cmdCode)
	}
}
