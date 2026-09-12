package op

import "testing"

func TestValidateChannelBaseURL(t *testing.T) {
	if err := validateChannelBaseURL("https://api.example.com/v1"); err != nil {
		t.Fatalf("https url: %v", err)
	}
	if err := validateChannelBaseURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("http url: %v", err)
	}
	if err := validateChannelBaseURL("ftp://x"); err == nil {
		t.Fatal("ftp should be rejected")
	}
	if err := validateChannelBaseURL("not-a-url"); err == nil {
		t.Fatal("bare host should be rejected")
	}
	if err := validateChannelBaseURL(""); err == nil {
		t.Fatal("empty should be rejected")
	}
	if err := validateChannelBaseURL("https://user:pass@example.com"); err == nil {
		t.Fatal("userinfo should be rejected")
	}
}
