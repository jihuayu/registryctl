package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestIntrospectionRejectsWrongBoundaryAndRevokedTokens(t *testing.T) {
	for _, change := range []string{"valid", "aud", "iss", "group_id", "exp", "active", "sub"} {
		t.Run(change, func(t *testing.T) {
			var issuer string
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.Form.Get("client_id") != "gateway" || r.Form.Get("client_secret") != "fixture-secret" || r.Form.Get("token") != "access" {
					t.Error("introspection authentication incorrect")
				}
				out := map[string]any{"active": true, "iss": issuer, "aud": "registry", "group_id": "g", "sub": "service:ci", "machine": true, "scope": "registry.push", "exp": time.Now().Add(time.Minute).Unix()}
				switch change {
				case "aud", "iss", "group_id":
					out[change] = "wrong"
				case "exp":
					out[change] = 0
				case "active":
					out[change] = false
				case "sub":
					out[change] = ""
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer h.Close()
			issuer = h.URL
			c := Client{Issuer: issuer, Audience: "registry", Group: "g", ClientID: "gateway", Secret: "fixture-secret", HTTP: h.Client()}
			i, e := c.Authenticate(context.Background(), "access")
			if change == "valid" {
				if e != nil || !i.Machine || i.Subject != "service:ci" {
					t.Fatal(i, e)
				}
			} else if e == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}

func TestLiveMachineAndInspector(t *testing.T) {
	issuer := os.Getenv("TEST_REGISTRY_ACCOUNT_ISSUER")
	if issuer == "" {
		t.Skip("需要实际 Account 隔离机器凭据")
	}
	c := Client{Issuer: issuer, Audience: os.Getenv("TEST_REGISTRY_ACCOUNT_AUDIENCE"), Group: "default", ClientID: os.Getenv("TEST_REGISTRY_GATEWAY_CLIENT_ID"), Secret: os.Getenv("TEST_REGISTRY_GATEWAY_SECRET")}
	token, e := c.Machine(context.Background(), os.Getenv("TEST_REGISTRY_MACHINE_CLIENT_ID"), os.Getenv("TEST_REGISTRY_MACHINE_SECRET"), "registry.pull registry.push")
	if e != nil {
		t.Fatal(e)
	}
	claims, e := c.Authenticate(context.Background(), token.Access)
	if e != nil || !claims.Machine || claims.Subject != "service:"+os.Getenv("TEST_REGISTRY_MACHINE_CLIENT_ID") {
		t.Fatal("machine or introspection mismatch", e)
	}
}
func TestMachineGrantUsesAccountAudience(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("audience") != "registry" || r.Form.Get("scope") != "registry.pull" {
			t.Fatal("incorrect machine grant")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 300})
	}))
	defer h.Close()
	c := Client{Issuer: h.URL, Audience: "registry", HTTP: h.Client()}
	token, e := c.Machine(context.Background(), "ci", "fixture-secret", "registry.pull")
	if e != nil || token.Access != "access" {
		t.Fatal(token, e)
	}
}
