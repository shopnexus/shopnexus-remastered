package pgxpool

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type QueryLogger struct{}

type contextKey string

const traceKey contextKey = "tracedata"

type TraceData struct {
	sql       string
	args      []interface{}
	startTime time.Time
}

func (ql *QueryLogger) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	startTime := time.Now()
	return context.WithValue(ctx, traceKey, TraceData{
		sql:       data.SQL,
		args:      data.Args,
		startTime: startTime,
	})
}

func (ql *QueryLogger) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	traceData, ok := ctx.Value(traceKey).(TraceData)
	if ok {
		elapsed := time.Since(traceData.startTime)
		queryName := strings.Split(traceData.sql, "\n")[0]
		queryName = strings.TrimPrefix(queryName, "-- name: ")
		log.Printf("🔨 - %s %v (%sms)\n", queryName, traceData.args, elapsed)
	}
}

func NewPgxTracer() pgx.QueryTracer {
	return &QueryLogger{}
}
