package db

import (
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"

	"github.com/switchlyprotocol/midgard/internal/util/miderr"
	"github.com/switchlyprotocol/midgard/internal/util/midlog"
)

func GetMigrateUpdates(currentDdlHash md5Hash, tag string) (data []byte, err error) {
	currentDdlHashString := hex.EncodeToString(currentDdlHash[:])

	// Get the current chain id
	status, err := getThorNodeStatus()
	if err != nil {
		midlog.FatalE(err, "ThorNode status query failed")
	}

	current := FullyQualifiedChainId{
		Name:        status.NodeInfo.Network,
		StartHash:   PrintableHash(string(status.SyncInfo.LatestBlockHash)),
		StartHeight: status.SyncInfo.EarliestBlockHeight,
	}
	root := EnrichAndGetRoot(&current)

	if root.Name != "thorchain" {
		midlog.Info("Skipping migration for non-mainnet")
		return nil, nil
	}

	latestHeight, err := GetLatestHeight(context.Background())
	if err != nil {
		midlog.FatalE(err, "Couldn't get latest height")
	}

	// v2.32.1 migration from v2.32.2
	if currentDdlHashString == "7da33d35fbcdb6f9ad375e199ba722e8" {

		// Trim migration for v2.32.2
		if latestHeight > 20996000 {
			midlog.Info("Trimming DB to height 20996001")
			TrimDB(context.Background(), 20996001)
		}

		data = []byte("UPDATE constants SET value = E'\\\\x741ae065783c76df8218e3a75298d633' WHERE key = 'ddl_hash';")
		currentDdlHashString = "741ae065783c76df8218e3a75298d633"
		latestHeight = 20996000
	}

	// v2.32.2 migration from v2.32.3
	if currentDdlHashString == "741ae065783c76df8218e3a75298d633" {
		// Trim migration for v2.32.3
		if latestHeight > 20996000 {
			midlog.Info("Trimming DB to height 20996001")
			TrimDB(context.Background(), 20996001)
		}

		data = []byte("UPDATE constants SET value = E'\\\\x38cc2cea31c31512f4d02ee13e36a91e' WHERE key = 'ddl_hash';")
		currentDdlHashString = "38cc2cea31c31512f4d02ee13e36a91e"
		latestHeight = 20996000
	}

	return data, nil
}

// TrimDB deletes all blocks including and after certain height.
// NOTE: You should trim to blocks height % 100 == 0 for now.

func TrimDB(ctx context.Context, heightOrTimestamp int64) {

	height, timestamp, err := QueryTimestampAndHeight(ctx, heightOrTimestamp)
	if err != nil {
		midlog.FatalF("Couldn't find height for %d", heightOrTimestamp)
	}

	// Actions & Rune Price Aggregates
	midlog.Info("Deleting actions")
	DeleteAfter("midgard_agg.actions", "block_timestamp", timestamp.ToI())
	DeleteAfter("midgard_agg.rune_price", "block_timestamp", timestamp.ToI())
	midlog.Info("Deleting watermark")
	DeleteWatermark(timestamp.ToI())

	midlog.InfoF("Deleting rows including and after height %d , timestamp %d", height, timestamp)
	tables := GetTableColumns(ctx)
	for table, columns := range tables {
		if columns["block_timestamp"] {
			midlog.InfoF("%s  deleting by block_timestamp", table)
			DeleteAfter(table, "block_timestamp", timestamp.ToI())
		} else if columns["height"] {
			midlog.InfoF("%s deleting by height", table)
			DeleteAfter(table, "height", height)
		} else if table == "constants" {
			midlog.InfoF("Skipping table %s", table)
		} else {
			midlog.WarnF("talbe %s has no good column", table)
		}
	}
}

func GetLatestHeight(ctx context.Context) (int64, error) {
	q := `
		SELECT height
		FROM block_log
		ORDER BY height DESC
		LIMIT 1
	`
	rows, err := Query(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, miderr.BadRequestF("No blocks in block_log")
	}

	var height int64
	err = rows.Scan(&height)
	return height, err
}

func QueryTimestampAndHeight(ctx context.Context, id int64) (
	height int64, timestamp Nano, err error) {
	q := `
		SELECT height, timestamp
		FROM block_log
		WHERE height=$1 OR timestamp<=$1
		ORDER BY TIMESTAMP DESC
		LIMIT 1
	`
	rows, err := Query(ctx, q, id)
	if err != nil {
		return
	}
	defer rows.Close()

	if !rows.Next() {
		err = miderr.BadRequestF("No such height or timestamp: %d", id)
		return
	}
	err = rows.Scan(&height, &timestamp)
	return
}

func DeleteWatermark(value int64) {
	q := `
	UPDATE 
		midgard_agg.watermarks 
	SET watermark = $1 
	WHERE materialized_table = 'actions' OR materialized_table = 'rune_price'
	`
	_, err := TheDB.Exec(q, value)
	if err != nil {
		midlog.FatalE(err, "update failed")
	}
}

func DeleteAfter(table string, columnName string, value int64) {
	q := fmt.Sprintf("DELETE FROM %s WHERE $1 <= %s", table, columnName)
	_, err := TheDB.Exec(q, value)
	if err != nil {
		midlog.FatalE(err, "delete failed")
	}
}

type TableMap map[string]map[string]bool

func GetTableColumns(ctx context.Context) TableMap {
	q := `
	SELECT
		table_name,
		column_name
	FROM information_schema.columns
	WHERE table_schema='midgard'
	`
	rows, err := Query(ctx, q)
	if err != nil {
		midlog.FatalE(err, "Query error")
	}
	defer rows.Close()

	ret := TableMap{}
	for rows.Next() {
		var table, column string
		err := rows.Scan(&table, &column)
		if err != nil {
			midlog.FatalE(err, "Query error")
		}
		if _, ok := ret[table]; !ok {
			ret[table] = map[string]bool{}
		}
		ret[table][column] = true
	}
	return ret
}
