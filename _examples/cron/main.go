package main

import (
	"context"
	"log/slog"
)

//zero:provider
func NewCronJobs(log *slog.Logger) *CronJobs { return &CronJobs{log: log} }

type CronJobs struct {
	log *slog.Logger
}

//zero:cron 5s
func (c *CronJobs) Ping(ctx context.Context) error {
	c.log.Info("Ping")
	return nil
}

var cli struct{}

func main() {

}
