package discuss

import (
	"fmt"
	"net/http"
)

func (s *DiscussService) GetTailscaleUserEmail(r *http.Request) (string, error) {
	user, err := s.tailClient.WhoIs(r.Context(), r.RemoteAddr)
	if err != nil {
		return "", err
	}

	s.logger.Debug(fmt.Sprintf("USER: %v", user.UserProfile.LoginName))
	return user.UserProfile.LoginName, nil
}
