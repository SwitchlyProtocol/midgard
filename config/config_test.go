package config_test

import (
	"testing"

	"github.com/switchlyprotocol/midgard/config"
	"github.com/switchlyprotocol/midgard/internal/db/testdb"
)

func TestMustLoadConfigFile(t *testing.T) {
	testdb.HideTestLogs(t)

	var c config.Config
	config.MustLoadConfigFiles("config.json", &c)
	config.LogAndcheckUrls(&c)
}
