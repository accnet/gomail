package realtime

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const Channel = "gomail.events"

type Event struct {
	Type   string    `json:"type"`
	UserID uuid.UUID `json:"user_id"`
	Data   any       `json:"data"`
}

type Publisher struct {
	client *redis.Client
}

func NewRedis(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
}

func NewPublisher(client *redis.Client) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) Publish(ctx context.Context, event Event) error {
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.client.Publish(ctx, Channel, b).Err()
}
