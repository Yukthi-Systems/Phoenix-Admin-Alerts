// Copyright (C) 2026 Yukthi Systems Private Limited
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License version 3
// as published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// version 3 along with this program. If not, see
// <https://www.gnu.org/licenses/>.

// Phoenix Admin Alerts is a cron-scheduled service that monitors mailbox
// and organization quotas and password expiry states, and publishes alert
// notifications to RabbitMQ for delivery.
//
// Configuration is read entirely from environment variables (optionally
// loaded from a ".env" file in the working directory); see the project
// README for the full list.
//
// By default the binary registers a cron job (schedule controlled by
// CRON_SCHEDULE) and runs indefinitely. Pass --once, -once, or set
// RUN_ONCE=true to perform a single processing pass and exit.
package main

import (
	"log/slog"
	"os"

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/database"
	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/logger"
	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/service"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
)

func main() {
	// Load environment variables from .env file if it exists
	_ = godotenv.Load()

	// Initialize JSON logging
	logger.Init()

	slog.Info("starting phoenix-admin-alerts application")

	// Initialize database connection pool
	db := database.New()
	defer db.Close()

	// Initialize application
	app, err := service.New(db)
	if err != nil {
		slog.Error("failed to initialize service", "error", err)
		os.Exit(1)
	}
	defer app.Close()

	// Run health check
	health := app.HealthCheck()
	slog.Info("database health check", "status", health["status"])

	// Check if run once is requested via environment variable or command-line flag
	runOnce := os.Getenv("RUN_ONCE") == "true"
	for _, arg := range os.Args[1:] {
		if arg == "--once" || arg == "-once" {
			runOnce = true
			break
		}
	}

	if runOnce {
		slog.Info("running one-time execution")
		app.Start()
		slog.Info("one-time execution completed")
		return
	}

	// Create cron scheduler
	c := cron.New(
		cron.WithChain(
			cron.SkipIfStillRunning(cron.DefaultLogger),
		),
	)

	// Schedule is configured via CRON_SCHEDULE; defaults to 3 AM daily.
	cronSchedule := os.Getenv("CRON_SCHEDULE")
	if cronSchedule == "" {
		cronSchedule = "0 3 * * *" // Default to 3 AM daily if not set
	}

	_, err = c.AddFunc(cronSchedule, func() {
		slog.Info("cron job started")

		app.Start()

		slog.Info("cron job completed")
	})
	if err != nil {
		slog.Error("failed to register cron job", "error", err)
		os.Exit(1)
	}

	c.Start()
	slog.Info("cron scheduler started")

	// Keep the application running
	select {}
}
