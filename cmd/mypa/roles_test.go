package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/telegram"
)

// V4-01: one binary with roles. These tests pin what each role runs and serves.

func TestParseRole(t *testing.T) {
	for _, name := range []string{"all", "ingress", "worker"} {
		r, err := parseRole(name)
		require.NoError(t, err, name)
		assert.Equal(t, role(name), r)
	}
	// The services the roles replaced are not roles, and nothing else is either.
	for _, name := range []string{"", "gateway", "orchestrator", "proxy", "ALL", "both"} {
		_, err := parseRole(name)
		assert.Error(t, err, "%q must be refused", name)
	}
}

func TestRole_WhatEachRoleRuns(t *testing.T) {
	assert.True(t, roleAll.servesIngress())
	assert.True(t, roleAll.runsWorker())
	assert.True(t, roleIngress.servesIngress())
	assert.False(t, roleIngress.runsWorker())
	assert.False(t, roleWorker.servesIngress())
	assert.True(t, roleWorker.runsWorker())
}

func telegramUpdate(text string) []byte {
	return []byte(`{"update_id": 1, "message": {"message_id": 7, "from": {"id": 987654321, "is_bot": false,
		"first_name": "Test"}, "chat": {"id": 987654321, "type": "private"}, "date": 1690000000, "text": "` + text + `"}}`)
}

func telegramRequest(url, text string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(telegramUpdate(text)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	return req
}

// Each role keeps exactly the surface of the service it replaces, built from the real routers.
// `all` is the proxy's public edge in front of both, so the dashboard and the webhooks reach one
// address: GraphQL and the webhooks are served by the same handler.
func TestRouterForRole_EachRoleKeepsTheSurfaceOfTheServiceItReplaces(t *testing.T) {
	t.Setenv("ENV", "development") // the playground is served only where the orchestrator served it

	pub := new(MockEventPublisher)
	pub.On("Publish", mock.Anything, mock.MatchedBy(func(msg channel.Inbound) bool {
		return msg.Sender == "987654321" && msg.Text == "hello"
	})).Return(nil)
	ingressRouter := setupIngressRouter(pub, testChannels(t, &config.Config{Channels: adapters.Config{Telegram: telegram.Config{SecretToken: testTelegramSecret}}}))
	appRouter := setupAppRouter(nil, nil, nil, &db.Client{}, nil, graph.UsageSettings{})

	serve := func(t *testing.T, r role, req *http.Request) int {
		t.Helper()
		h, err := routerForRole(r, ingressRouter, appRouter)
		require.NoError(t, err)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	get := func(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }
	gql := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{__typename}"}`))
	}

	t.Run("all", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, serve(t, roleAll, telegramRequest("/webhook/telegram", "hello")), "webhooks reach the ingress")
		pub.AssertNumberOfCalls(t, "Publish", 1)
		assert.Equal(t, http.StatusUnauthorized, serve(t, roleAll, gql()), "GraphQL is served, behind the edge's guard")
		badToken := gql()
		badToken.Header.Set("Authorization", "Bearer not-a-valid-token")
		assert.Equal(t, http.StatusUnauthorized, serve(t, roleAll, badToken), "and behind the auth middleware")
		assert.Equal(t, http.StatusBadRequest, serve(t, roleAll, get("/auth/google/callback")), "the Google sign-in callback is served")
		assert.Equal(t, http.StatusNotFound, serve(t, roleAll, get("/playground")), "the playground is not public")
		assert.Equal(t, http.StatusNotFound, serve(t, roleAll, get("/health")), "the health check is not public")
	})

	t.Run("ingress", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, serve(t, roleIngress, telegramRequest("/webhook/telegram", "hello")))
		pub.AssertNumberOfCalls(t, "Publish", 2)
		assert.Equal(t, http.StatusOK, serve(t, roleIngress, get("/health")))
		assert.Equal(t, http.StatusNotFound, serve(t, roleIngress, gql()), "an ingress serves no GraphQL")
		assert.Equal(t, http.StatusNotFound, serve(t, roleIngress, get("/auth/google/callback")))
		assert.Equal(t, http.StatusNotFound, serve(t, roleIngress, get("/playground")))
	})

	t.Run("worker", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, serve(t, roleWorker, telegramRequest("/webhook/telegram", "hello")), "a worker takes no webhooks")
		pub.AssertNumberOfCalls(t, "Publish", 2)
		assert.Equal(t, http.StatusUnauthorized, serve(t, roleWorker, gql()))
		assert.Equal(t, http.StatusBadRequest, serve(t, roleWorker, get("/auth/google/callback")))
		assert.Equal(t, http.StatusOK, serve(t, roleWorker, get("/playground")), "outside production, as the orchestrator did")
	})
}

func TestRouterForRole_RefusesAMissingSide(t *testing.T) {
	h := http.NotFoundHandler()
	_, err := routerForRole(roleAll, nil, h)
	assert.Error(t, err)
	_, err = routerForRole(roleIngress, nil, h)
	assert.Error(t, err)
	_, err = routerForRole(roleWorker, h, nil)
	assert.Error(t, err)
	_, err = routerForRole(role("gateway"), h, h)
	assert.Error(t, err)
}

// A role refuses to start on an incomplete configuration, before it connects to anything.
func TestRun_RefusesAnInvalidConfiguration(t *testing.T) {
	for _, r := range []role{roleAll, roleIngress, roleWorker} {
		waited := false
		code := run(&config.Config{}, r, func(<-chan error) error { waited = true; return nil })
		assert.Equal(t, 1, code, string(r))
		assert.False(t, waited, "%s must not serve anything", r)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The durable hand-off survives the merge: the process that receives a webhook publishes it to the
// real inbound.messages queue and answers, and the message waits there for a worker. This runs the
// production `run` with the ingress role against a real RabbitMQ; the default role routes the
// same webhooks to the same ingress (TestRouterForRole_EachRoleKeepsTheSurfaceOfTheServiceItReplaces).
func TestRun_IngressHandsWebhooksToTheDurableQueue(t *testing.T) {
	amqpURL := rabbitMQ(t)
	declareAppQueues(t, amqpURL)
	cfg := &config.Config{
		RabbitMQ: config.RabbitMQConfig{URL: amqpURL},
		Channels: adapters.Config{Telegram: telegram.Config{SecretToken: testTelegramSecret}},
		Server:   config.ServerConfig{Port: freePort(t)},
	}

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

	url := fmt.Sprintf("http://127.0.0.1:%d/webhook/telegram", cfg.Server.Port)
	var resp *http.Response
	require.Eventually(t, func() bool {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(telegramUpdate("queued, not processed")))
		if err != nil {
			return false
		}
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
		resp, err = http.DefaultClient.Do(req)
		return err == nil
	}, 30*time.Second, 100*time.Millisecond, "the app must start listening on its port")
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// The message is in the durable queue. Taking it off also leaves the shared broker empty for
	// the restore tests, which require drained queues.
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
	}, 10*time.Second, 50*time.Millisecond, "the webhook must have been published to %s", broker.InboundMessagesQueue)
	var msg channel.Inbound
	require.NoError(t, json.Unmarshal(delivery.Body, &msg))
	assert.Equal(t, "987654321", msg.Sender)
	assert.Equal(t, "queued, not processed", msg.Text)

	close(stop)
	select {
	case code := <-exit:
		assert.Equal(t, 0, code, "a clean shutdown exits 0")
	case <-time.After(30 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// A port that is already taken is a failed start, not a silent one: run exits non-zero.
func TestRun_ExitsNonZeroWhenThePortIsTaken(t *testing.T) {
	amqpURL := rabbitMQ(t)
	declareAppQueues(t, amqpURL)
	taken, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer taken.Close()

	cfg := &config.Config{
		RabbitMQ: config.RabbitMQConfig{URL: amqpURL},
		Server:   config.ServerConfig{Port: taken.Addr().(*net.TCPAddr).Port},
	}
	done := make(chan int, 1)
	go func() { done <- run(cfg, roleIngress, waitForSignal) }()
	select {
	case code := <-done:
		assert.Equal(t, 1, code)
	case <-time.After(30 * time.Second):
		t.Fatal("run kept going on a port it could not listen on")
	}
}
