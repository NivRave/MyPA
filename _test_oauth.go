package main

import (
	"fmt"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/calendar"
)

func main() {
	cfg := &config.GoogleConfig{
		ClientID: "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL: "http://localhost:8081/auth/google/callback",
	}
	oauthCfg := calendar.NewOAuthConfig(cfg)
	fmt.Println(oauthCfg.AuthCodeURL("12345"))
}
