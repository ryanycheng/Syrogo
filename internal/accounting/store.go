package accounting

import (
	"context"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Store interface {
	Record(runtime.UsageRecord)
	Query(Query) ([]StatsItem, error)
	Coverage() Coverage
	RecentRecords(RecentRecordsQuery) ([]runtime.UsageRecord, error)
	Close(context.Context) error
}
