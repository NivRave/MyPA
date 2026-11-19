package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/deploy"
)

// fakeBotAPI is the Telegram Bot API on a fake transport: it holds one update until it is
// confirmed and records which methods were called. Anything but the polling methods fails the test.
type fakeBotAPI struct {
	t       *testing.T
	token   string
	mu      sync.Mutex
	calls   []string
	pending []byte // the update Telegram holds; nil once confirmed
}

func (f *fakeBotAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	method := strings.TrimPrefix(req.URL.Path, "/bot"+f.token+"/")
	f.calls = append(f.calls, method)
	if req.URL.Host != "api.telegram.org" || method == req.URL.Path {
		f.t.Errorf("unexpected request %s %s", req.Method, req.URL.Redacted())
		return nil, fmt.Errorf("refused")
	}
	reply := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}
	switch method {
	case "deleteWebhook":
		return reply(`{"ok":true,"result":true}`)
	case "getUpdates":
		var params struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(req.Body).Decode(&params)
		if params.Offset > 1 {
			f.pending = nil // confirmed
		}
		if f.pending == nil {
			f.mu.Unlock()
			time.Sleep(10 * time.Millisecond) // a long poll with nothing waiting
			f.mu.Lock()
			return reply(`{"ok":true,"result":[]}`)
		}
		return reply(`{"ok":true,"result":[` + string(f.pending) + `]}`)
	}
	f.t.Errorf("a Telegram-only install with no tunnel must not call %s", method)
	return reply(`{"ok":false,"description":"unexpected"}`)
}

func (f *fakeBotAPI) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// telegramOnlyEnv is the .env of a Telegram-only install with no tunnel: a bot token and nothing
// about how the install is reached. No secret token either: no webhook is served.
func telegramOnlyEnv(amqpURL string, port int) func(string) string {
	env := map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:no-tunnel",
		"RABBITMQ_URL":       amqpURL,
		"HTTP_PORT":          fmt.Sprint(port),
	}
	return func(k string) string { return env[k] }
}

// The "done when" of V4-40, second half: a Telegram-only install runs with no tunnel. Its settings
// name no tunnel and no public address, they validate for every role, and the production `run`
// receives a message sent on Telegram (by polling the Bot API, here a fake) into the durable
// inbound queue of a real RabbitMQ. Nothing registers a webhook and no Telegram route is served.
func TestRun_TelegramOnlyInstallRunsWithNoTunnel(t *testing.T) {
	amqpURL := rabbitMQ(t)
	declareAppQueues(t, amqpURL)
	port := freePort(t)
	getenv := telegramOnlyEnv(amqpURL, port)

	cfg := &config.Config{
		RabbitMQ: config.RabbitMQConfig{URL: amqpURL},
		Server:   config.ServerConfig{Port: port},
		Channels: adapters.LoadConfig(getenv),
		Deploy:   deploy.Load(getenv),
	}
	require.False(t, cfg.Deploy.Reachable(), "nothing reaches this install")
	require.True(t, cfg.Channels.Telegram.Polling)
	require.NoError(t, cfg.Validate(string(roleIngress)))
	full := *cfg
	full.Server = config.ServerConfig{Port: port, DashboardAPISecret: "d", DashboardFamilyGroup: "f", JWTSecret: "j"}
	full.Encryption, full.Database, full.Redis = config.EncryptionConfig{Key: "e"}, config.DatabaseConfig{URL: "postgres://x"}, config.RedisConfig{URL: "redis://x"}
	full.Gemini = config.GeminiConfig{APIKey: "g"}
	require.NoError(t, full.Validate(string(roleAll)), "the default role accepts it too")

	api := &fakeBotAPI{t: t, token: "123:no-tunnel", pending: telegramUpdate("sent with no tunnel")}
	channelOptions = []adapters.Option{adapters.WithHTTPClient(&http.Client{Transport: api, Timeout: 5 * time.Second})}
	t.Cleanup(func() { channelOptions = nil })

	stop := make(chan struct{})
	exit := make(chan int, 1)
	go func() {
		exit <- run(cfg, roleIngress, func(serverErr <-chan error) error {
			select {
			case <-stop:
				return nil
			case err := <-serverErr:
				return err
			}
		})
	}()

	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()
	var delivery amqp.Delivery
	require.Eventually(t, func() bool {
		var ok bool
		delivery, ok, err = ch.Get(broker.InboundMessagesQueue, true)
		return err == nil && ok
	}, 30*time.Second, 50*time.Millisecond, "the polled message must reach %s", broker.InboundMessagesQueue)
	var msg channel.Inbound
	require.NoError(t, json.Unmarshal(delivery.Body, &msg))
	assert.Equal(t, "telegram", msg.Channel)
	assert.Equal(t, "987654321", msg.Sender)
	assert.Equal(t, "sent with no tunnel", msg.Text)

	// The webhook route does not exist on a polling install.
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/webhook/telegram", port), "application/json", bytes.NewReader(telegramUpdate("forged")))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	calls := api.called()
	require.NotEmpty(t, calls)
	assert.Equal(t, "deleteWebhook", calls[0], "the webhook is removed before polling")
	assert.NotContains(t, calls, "setWebhook")

	close(stop)
	select {
	case code := <-exit:
		assert.Equal(t, 0, code, "a clean shutdown exits 0")
	case <-time.After(30 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// ADR 0003 §6: a configured channel that needs a public address stops an install that has none,
// before it connects to anything, with a message naming the channel. The broker address is a
// listener that records every connection attempt, so the refusal must come before the ingress
// tries to reach RabbitMQ, not from that attempt failing.
func TestRun_RefusesAWebhookChannelWithoutAPublicAddress(t *testing.T) {
	broker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer broker.Close()
	var attempts atomic.Int32
	go func() {
		for {
			conn, err := broker.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_ = conn.Close()
		}
	}()

	env := map[string]string{"TWILIO_ACCOUNT_SID": "AC1", "TWILIO_AUTH_TOKEN": "tok", "TWILIO_FROM_NUMBER": "whatsapp:+12025550199"}
	getenv := func(k string) string { return env[k] }
	cfg := &config.Config{
		RabbitMQ: config.RabbitMQConfig{URL: "amqp://nobody:x@" + broker.Addr().String() + "/"},
		Channels: adapters.LoadConfig(getenv),
		Deploy:   deploy.Load(getenv),
	}
	waited := false
	assert.Equal(t, 1, run(cfg, roleIngress, func(<-chan error) error { waited = true; return nil }))
	assert.False(t, waited, "nothing may be served")
	assert.Zero(t, attempts.Load(), "run must refuse before it connects to the broker")

	err = checkReachable(cfg.Deploy, testChannels(t, cfg))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whatsapp")
	assert.Contains(t, err.Error(), "INGRESS")

	env["INGRESS"], env["PUBLIC_URL"] = "proxy", "https://mypa.example.com"
	cfg.Channels, cfg.Deploy = adapters.LoadConfig(getenv), deploy.Load(getenv)
	assert.NoError(t, checkReachable(cfg.Deploy, testChannels(t, cfg)), "with a public address it runs")
}
