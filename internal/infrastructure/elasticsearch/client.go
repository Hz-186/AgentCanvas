package elasticsearch

import (
	"context"

	"agentcanvas/internal/pkg/config"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
)

func New(cfg config.ElasticsearchConfig) (*elasticsearch.Client, error) {
	return elasticsearch.NewClient(elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
	})
}

func Ping(ctx context.Context, client *elasticsearch.Client) error {
	res, err := client.Info(client.Info.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return err
	}
	return nil
}
