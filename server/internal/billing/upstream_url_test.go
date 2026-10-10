package billing

import (
	"reflect"
	"testing"
)

func TestNormalizeUpstreamURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" HTTPS://API.Example.com:443/v1/// ", "https://api.example.com/v1"},
		{"http://api.example.com:80/", "http://api.example.com"},
		{"https://api.example.com:8443/Case", "https://api.example.com:8443/Case"},
		{"https://[::1]:443/v1/", "https://[::1]/v1"},
		{"https://example.com/a%2Fb/", "https://example.com/a%2Fb"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeUpstreamURL(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v", got, err)
			}
		})
	}
	for _, raw := range []string{"", "example.com", "ftp://example.com", "https://user:pass@example.com", "https://example.com?key=x", "https://example.com#x", "https://example.com?", "https://example.com#", "https://example.com:0", "https://example.com:99999", "https://example.com/a b", "https:///v1"} {
		if _, err := NormalizeUpstreamURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestMatchUpstreamChannelsExactEndpoint(t *testing.T) {
	channels := []ConfiguredChannel{
		{ChannelID: 2, ChannelName: "two", BaseURL: " HTTPS://API.example.com:443/v1/ ", Models: "a,b", Status: 2},
		{ChannelID: 1, ChannelName: "one", BaseURL: "https://api.example.com/v1"},
		{ChannelID: 2, BaseURL: "https://api.example.com/v1"},
		{ChannelID: 3, BaseURL: "https://api.example.com.evil/v1"},
		{ChannelID: 4, BaseURL: "https://api.example.com/v10"},
		{ChannelID: 5, BaseURL: "http://api.example.com/v1"},
		{ChannelID: 6, BaseURL: "https://api.example.com:8443/v1"},
		{ChannelID: 7, BaseURL: ""},
	}
	got := MatchUpstreamChannels("https://api.example.com/v1", channels)
	want := []UpstreamChannel{{ChannelID: 1, ChannelName: "one"}, {ChannelID: 2, ChannelName: "two"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if len(MatchUpstreamChannels("", channels)) != 0 {
		t.Fatal("empty URL matched")
	}
}
