package exchange

import "time"
import "github.com/iancoleman/orderedmap"

type Log struct {
	ts     time.Time
	labels *orderedmap.OrderedMap
}

func NewLog(ts time.Time, labels *orderedmap.OrderedMap) Log {
	return Log{
		ts:     ts,
		labels: labels,
	}
}

func (l *Log) TimeStamp() time.Time {
	return l.ts
}

func (l *Log) Labels() *orderedmap.OrderedMap {

	return copyMap(l.labels)
}
