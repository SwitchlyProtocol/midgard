package miderr

import "github.com/switchlyprotocol/midgard/internal/util/midlog"

func LogEventParseErrorF(format string, v ...interface{}) {
	midlog.WarnF(format, v...)
}
