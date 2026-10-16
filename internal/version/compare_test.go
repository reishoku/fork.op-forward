package version

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.3.0", "0.3.1", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"dev", "0.1.0", -1},
		{"dev", "dev", 0},
		{"0.1.0", "dev", 1},
		{"v0.3.0", "0.3.0", 0},
		// SemVer pre-release and build suffixes do not affect the comparison.
		{"0.8.0-rc.1", "0.8.0", 0},
		{"0.8.0+build.5", "0.7.9", 1},
		{"v0.8.1-rc.1+meta", "0.8.0", 1},
		// More than three components is not a version.
		{"0.7.3.1", "0.1.0", -1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := Compare(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCheckCompatibility(t *testing.T) {
	tests := []struct {
		name            string
		client, server  string
		min             string
		wantUpgrade     bool
		wantAvailable   bool
		wantMsgContains string
	}{
		{
			name:          "current client",
			client:        "0.3.0",
			server:        "0.3.0",
			min:           "0.1.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
		{
			name:          "slightly old client gets update notice",
			client:        "0.2.0",
			server:        "0.3.0",
			min:           "0.1.0",
			wantUpgrade:   false,
			wantAvailable: true,
		},
		{
			name:            "client below minimum is rejected",
			client:          "0.1.0",
			server:          "0.3.0",
			min:             "0.2.0",
			wantUpgrade:     true,
			wantAvailable:   true,
			wantMsgContains: "below minimum",
		},
		{
			name:          "dev client is never rejected",
			client:        "dev",
			server:        "0.3.0",
			min:           "0.2.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
		{
			name:          "empty client version is never rejected",
			client:        "",
			server:        "0.3.0",
			min:           "0.2.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
		{
			name:          "client with a SemVer suffix is compared by its core",
			client:        "0.1.0-rc.1",
			server:        "0.3.0",
			min:           "0.2.0",
			wantUpgrade:   true,
			wantAvailable: true,
		},
		{
			name:          "suffixed current client passes the minimum",
			client:        "0.3.0+build.7",
			server:        "0.3.0",
			min:           "0.1.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
		{
			// Comparing an unparseable version as 0.0.0 would put it below
			// every minimum and answer 426 to every request.
			name:          "unparseable client version is never rejected",
			client:        "0.7.3.1",
			server:        "0.3.0",
			min:           "0.2.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
		{
			name:          "newer client than server",
			client:        "0.4.0",
			server:        "0.3.0",
			min:           "0.1.0",
			wantUpgrade:   false,
			wantAvailable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgrade, available, msg := CheckCompatibility(tt.client, tt.server, tt.min)
			if upgrade != tt.wantUpgrade {
				t.Errorf("upgradeRequired = %v, want %v", upgrade, tt.wantUpgrade)
			}
			if (available != "") != tt.wantAvailable {
				t.Errorf("updateAvailable = %q, wantAvailable = %v", available, tt.wantAvailable)
			}
			if tt.wantMsgContains != "" && !contains(msg, tt.wantMsgContains) {
				t.Errorf("message = %q, want it to contain %q", msg, tt.wantMsgContains)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
