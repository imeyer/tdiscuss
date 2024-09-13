package main

import (
	"testing"
)

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    int64
		wantErr bool
	}{
		{"Valid thread ID", "/thread/1263", 1263, false},
		{"Valid member ID", "/member/23", 23, false},
		{"Invalid member path", "/member/abc", 0, true},
		{"Invalid thread path", "/thread/abc", 0, true},
		{"Empty path", "", 0, true},
		{"Missing thread ID", "/thread/", 0, true},
		{"Missing member ID", "/member/", 0, true},
		{"Extra thread characters", "/thread/123/extra", 0, true},
		{"Extra member characters", "/member/123/extra", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseID(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseID() = %v, want %v", got, tt.want)
			}
		})
	}
}
