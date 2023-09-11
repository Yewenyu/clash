package clash

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
*/
import "C"

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/Dreamacro/clash/config"
	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/hub/route"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel/statistic"
)

// framework support

func ReadConfig(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("Configuration file %s is empty", path)
	}
	return data, err
}

func GetRawCfgByPath(path string) (*config.RawConfig, error) {
	if len(path) == 0 {
		path = constant.Path.Config()
	}

	buf, err := ReadConfig(path)
	if err != nil {
		return nil, err
	}
	return config.UnmarshalRawConfig(buf)
}

func SetupHomeDir(homeDirPath string) {
	constant.SetHomeDir(homeDirPath)
}

func RunByConfig(configPath string, externalController string) error {
	log.Infoln("start run")
	constant.SetConfig(configPath)
	rawConfig, err := GetRawCfgByPath(configPath)
	if err != nil {
		return err
	}
	log.Infoln("current rawconfig mixedPort: %d", rawConfig.MixedPort)
	log.Infoln("current rawconfig mode: %d", rawConfig.Mode)
	rawConfig.ExternalUI = ""
	rawConfig.Profile.StoreSelected = false
	if len(externalController) > 0 {
		rawConfig.ExternalController = externalController
	}

	cfg, err := config.ParseRawConfig(rawConfig)
	if err != nil {
		log.Infoln("config.parse raw config failed by error: %s", err.Error())
		return err
	}
	go route.Start(cfg.General.ExternalController, cfg.General.Secret)
	executor.ApplyConfig(cfg, true)
	log.Infoln("apply config success")
	return nil

}

func CloseAllConnections() {
	snapshot := statistic.DefaultManager.Snapshot()
	for _, c := range snapshot.Connections {
		c.Close()
	}
}

func CustomLogFile(logPath string) {
	log.CustomLogPath(logPath)
}

func main() {

}
