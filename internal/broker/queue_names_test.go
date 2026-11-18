package broker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-5: the inbound queue carries Telegram AND WhatsApp messages, so it is no longer named after
// Telegram; queue names live in one place so publisher and consumer can never drift apart.
func TestQueueNames(t *testing.T) {
	assert.Equal(t, "inbound.messages", InboundMessagesQueue)
	assert.Equal(t, "audit.events", AuditEventsQueue)
}

// Every service must take queue names from the constants above, never from string literals.
func TestServicesUseQueueConstants(t *testing.T) {
	mains, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*", "main.go"))
	require.NoError(t, err)
	require.NotEmpty(t, mains)

	brokerCtors := map[string]bool{"NewSafePublisher": true, "NewConsumer": true, "NewConsumerWithOptions": true}
	calls := 0
	for _, path := range mains {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !brokerCtors[sel.Sel.Name] {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "broker" || len(call.Args) < 2 {
				return true
			}
			calls++
			_, isLiteral := call.Args[1].(*ast.BasicLit)
			assert.False(t, isLiteral, "%s: broker.%s must use a queue-name constant, not a literal", path, sel.Sel.Name)
			return true
		})
	}
	assert.GreaterOrEqual(t, calls, 4, "gateway, orchestrator (x2) and audit-worker create publishers/consumers")
}
