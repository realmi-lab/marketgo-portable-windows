package collector

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
)

const maxMessageSize = 16 * 1024 * 1024

type MessageType int

const (
	MessageText MessageType = iota + 1
	MessageBinary
)

type Connection interface {
	Read(context.Context) (MessageType, []byte, error)
	Write(context.Context, MessageType, []byte) error
	Ping(context.Context) error
	Close() error
}

type Dialer interface {
	Dial(context.Context, string) (Connection, error)
}

type WebSocketDialer struct {
	HTTPClient *http.Client
}

func (d WebSocketDialer) Dial(ctx context.Context, url string) (Connection, error) {
	connection, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:      d.HTTPClient,
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		return nil, err
	}
	connection.SetReadLimit(maxMessageSize)
	return &webSocketConnection{connection: connection}, nil
}

type webSocketConnection struct {
	connection *websocket.Conn
}

func (c *webSocketConnection) Read(ctx context.Context) (MessageType, []byte, error) {
	messageType, payload, err := c.connection.Read(ctx)
	return MessageType(messageType), payload, err
}

func (c *webSocketConnection) Write(ctx context.Context, messageType MessageType, payload []byte) error {
	return c.connection.Write(ctx, websocket.MessageType(messageType), payload)
}

func (c *webSocketConnection) Ping(ctx context.Context) error {
	return c.connection.Ping(ctx)
}

func (c *webSocketConnection) Close() error {
	return c.connection.Close(websocket.StatusNormalClosure, "collector stopped")
}
