package cli

import (
	"testing"
)

func TestBuildCmdCopyFlag(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantCopy     string
		wantArgsLen  int
		wantChanged  bool
	}{
		{
			name:        "copy flag without opt val",
			args:        []string{"--copy", "shipfile.py"},
			wantCopy:    ".",
			wantArgsLen: 1,
			wantChanged: true,
		},
		{
			name:        "copy short flag without opt val",
			args:        []string{"-c", "shipfile.py"},
			wantCopy:    ".",
			wantArgsLen: 1,
			wantChanged: true,
		},
		{
			name:        "copy flag with equals value",
			args:        []string{"--copy=/path/to/src", "shipfile.py"},
			wantCopy:    "/path/to/src",
			wantArgsLen: 1,
			wantChanged: true,
		},
		{
			name:        "copy flag with separate arg",
			args:        []string{"--copy", "/path/to/src", "shipfile.py"},
			wantCopy:    ".",
			wantArgsLen: 2,
			wantChanged: true,
		},
		{
			name:        "no copy flag",
			args:        []string{"shipfile.py"},
			wantCopy:    "",
			wantArgsLen: 1,
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newBuildCmd()
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags failed: %v", err)
			}
			copyVal, err := cmd.Flags().GetString("copy")
			if err != nil {
				t.Fatalf("GetString(copy) failed: %v", err)
			}
			if copyVal != tt.wantCopy {
				t.Errorf("got copy=%q, want %q", copyVal, tt.wantCopy)
			}
			if cmd.Flags().Changed("copy") != tt.wantChanged {
				t.Errorf("got changed=%v, want %v", cmd.Flags().Changed("copy"), tt.wantChanged)
			}
			args := cmd.Flags().Args()
			if len(args) != tt.wantArgsLen {
				t.Errorf("got args len=%d (%v), want %d", len(args), args, tt.wantArgsLen)
			}
		})
	}
}

