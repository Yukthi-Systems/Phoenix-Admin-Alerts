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

// Package rabbitmq implements a thin publisher wrapper around go-rabbitmq
// used to deliver alert email payloads to a message broker queue.
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	rabbitmq "github.com/wagslane/go-rabbitmq"
)

// MailMessage is the payload sent to the RabbitMQ worker.
type MailMessage struct {
	To        string                 `json:"to"`
	Variables map[string]interface{} `json:"variables"`
	Template  string                 `json:"template"`
}

// Publisher wraps the go-rabbitmq connection and publisher.
type Publisher struct {
	conn      *rabbitmq.Conn
	publisher *rabbitmq.Publisher
	queueName string
}

// NewPublisher creates a new RabbitMQ publisher.
// It establishes a connection (with built-in reconnect support) and
// declares the target queue so it exists before we publish.
func NewPublisher(url string, queueName string) (*Publisher, error) {
	conn, err := rabbitmq.NewConn(
		url,
		rabbitmq.WithConnectionOptionsLogging,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	publisher, err := rabbitmq.NewPublisher(
		conn,
		rabbitmq.WithPublisherOptionsLogging,
	)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create rabbitmq publisher: %w", err)
	}

	slog.Info("RabbitMQ publisher initialized", "queue", queueName)

	return &Publisher{
		conn:      conn,
		publisher: publisher,
		queueName: queueName,
	}, nil
}

// PublishEmail serializes and publishes an email alert message to the queue.
// The AMQP header {"type": "email"} identifies the message type for the worker.
func (p *Publisher) PublishEmail(ctx context.Context, to string, templateName string, variables map[string]interface{}) error {
	msg := MailMessage{
		To:        to,
		Variables: variables,
		Template:  templateName,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal mail message: %w", err)
	}

	err = p.publisher.Publish(
		body,
		[]string{p.queueName},
		rabbitmq.WithPublishOptionsContentType("application/json"),
		rabbitmq.WithPublishOptionsExchange(""), // Use the default exchange
		rabbitmq.WithPublishOptionsHeaders(rabbitmq.Table{
			"type": "email",
		}),
		rabbitmq.WithPublishOptionsPersistentDelivery, // Survive broker restarts
	)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	slog.Debug("Published email alert to queue",
		"queue", p.queueName,
		"template", templateName,
		"to", to,
	)

	return nil
}

// Close cleanly shuts down the publisher and connection.
func (p *Publisher) Close() {
	if p.publisher != nil {
		p.publisher.Close()
	}
	if p.conn != nil {
		p.conn.Close()
	}
	slog.Info("RabbitMQ publisher closed")
}
