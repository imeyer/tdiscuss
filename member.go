package main

import (
	"log/slog"
	"net/http"
)

func (s *DiscussService) GetTailscaleUserEmail(r *http.Request) (string, error) {
	user, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		s.logger.Debug("get tailscale user email", slog.String("error", err.Error()))
		return "", err
	}

	s.logger.Debug("get tailscale user email", slog.String("user", user.UserProfile.LoginName))
	return user.UserProfile.LoginName, nil
}
