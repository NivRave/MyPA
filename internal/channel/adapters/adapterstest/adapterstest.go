// Package adapterstest builds the production channel registry around mocked platform clients, for
// tests of the engine and of the code that sends through it.
package adapterstest

import (
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
)

// Registry returns the real adapters, unconfigured, sending and fetching through tg and wa. A nil
// client leaves that adapter on its real (unconfigured) client, which fails every call.
func Registry(tg telegram.API, wa twilio.API) *channel.Registry {
	reg, err := adapters.Build(adapters.Config{}, adapters.WithAPIs(tg, wa))
	if err != nil {
		panic(err) // the shipped adapters always register; a failure here is a programming error
	}
	return reg
}
