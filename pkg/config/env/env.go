package env

import (
	"net"

	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/telco/envconfig"
)

type EnvConfigs struct {
	ConfigAddr       string `mapstructure:"CONFIG_ADDR"`
	TargetGov        string `mapstructure:"TARGET_GOV"` // Governance URL in "host:port" format
	GovBackendPort   int    `mapstructure:"GOV_BACKEND_PORT"`
	Governance       bool   `mapstructure:"GOVERNANCE"` // Enable/disable governance registration
	GovFail          bool   `mapstructure:"GOV_FAIL"` // Fail application if governance registration fails
	ServiceIP        string `mapstructure:"SERVICE_IP"`
	ServicePort      int    `mapstructure:"SERVICE_PORT"`
	ConfdExpose      bool   `mapstructure:"CONFD_EXPOSE"`
	ConfdServiceAddr string `mapstructure:"CONFD_SERVICE_ADDR"`
}

func (e *EnvConfigs) DefaultValues() {
	e.TargetGov = "127.0.0.1:18000"
	e.Governance = true
	e.GovFail = true
	e.GovBackendPort = 2345
	e.ServiceIP = GetLocalIP()
	e.ServicePort = 3868
	e.ConfdExpose = false
}

func (e *EnvConfigs) Load() error {
	return envconfig.ReadConfigFrom("", e)
}

func (e *EnvConfigs) Print() {
	// No-op, handled elsewhere
	envconfig.Show(logger.Log.With("mod", "env").(logger.Logger), e)
}

func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return "0.0.0.0"
}
