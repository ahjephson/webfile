package thumbnailcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct {
	http  *http.Client
	token string
}

func NewClient(socketPath, token string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 30 * time.Second}, token: token}
}

func (c *Client) Get(ctx context.Context, key string) (Item, bool, error) {
	req, err := c.request(ctx, http.MethodGet, key, nil)
	if err != nil {
		return Item{}, false, err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Item{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Item{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return Item{}, false, fmt.Errorf("thumbnail cache get: %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxThumbnailBytes+1))
	if err != nil {
		return Item{}, false, err
	}
	if len(data) > maxThumbnailBytes {
		return Item{}, false, errors.New("thumbnail cache response is too large")
	}
	item := Item{Data: data, ContentType: response.Header.Get("Content-Type")}
	if err := validateItem(item); err != nil {
		return Item{}, false, err
	}
	return item, true, nil
}

func (c *Client) Put(ctx context.Context, key string, item Item) error {
	req, err := c.request(ctx, http.MethodPut, key, bytes.NewReader(item.Data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", item.ContentType)
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("thumbnail cache put: %s", response.Status)
	}
	return nil
}

func (c *Client) Move(ctx context.Context, from, to string) error {
	body, err := json.Marshal(struct {
		Key string `json:"key"`
	}{Key: to})
	if err != nil {
		return err
	}
	req, err := c.request(ctx, http.MethodPost, from+"/move", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("thumbnail cache move: %s", response.Status)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	req, err := c.request(ctx, http.MethodDelete, key, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("thumbnail cache delete: %s", response.Status)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, key string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://thumbnail-cache/cache/"+key, body)
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, err
}
