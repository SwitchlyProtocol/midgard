package main

import (
	"github.com/switchlyprotocol/midgard/config"
	"github.com/switchlyprotocol/midgard/internal/db"
	"github.com/switchlyprotocol/midgard/internal/util/midlog"

	_ "github.com/switchlyprotocol/midgard/internal/globalinit"
)

func main() {
	midlog.LogCommandLine()
	config.ReadGlobal()

	db.SetupWithoutUpdate()

	midlog.Warn("Destroying database by removing the ddl hash")
	_, err := db.TheDB.Exec(`DELETE FROM constants WHERE key = 'ddl_hash'`)
	if err != nil {
		midlog.FatalE(err, "Failed to delete ddl hash.")
	}
	midlog.Info("Done. Next midgard run will reload the DB schema.")
}
