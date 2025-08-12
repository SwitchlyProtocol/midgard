package dbinit

// The purpose of this package is to ensure that every binary that uses `internal/db` also depend
// on all packages that affect the DB schema.

import (
	"github.com/switchlyprotocol/midgard/internal/db"

	// Every package that calls `RegisterAggregate` should be included here
	_ "github.com/switchlyprotocol/midgard/internal/timeseries"
	_ "github.com/switchlyprotocol/midgard/internal/timeseries/stat"
)

func Setup() {
	db.SetupDoNotCallDirectly()
}
