package broker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
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
// V4-01: the app wires the broker outside main.go (ingress.go, worker.go), so every non-test Go
// file of every command is searched, not only its main.go.
func TestServicesUseQueueConstants(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*", "*.go"))
	require.NoError(t, err)
	var mains []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			mains = append(mains, f)
		}
	}
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
	assert.GreaterOrEqual(t, calls, 4, "the app's ingress, its worker (x2) and the audit-worker create publishers/consumers")
}
