package middleware

import (
	"context"
	"net/http"
)

// contextKey is an unexported type for context keys to prevent collisions
type boardDataContextKey struct{}

// BoardDataQuerier defines the interface for getting board data
type BoardDataQuerier interface {
	GetBoardData(ctx context.Context) (interface{}, error)
}

// BoardDataMiddleware fetches board data and adds it to the request context
func BoardDataMiddleware(querier BoardDataQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			
			// Fetch board data
			boardData, err := querier.GetBoardData(ctx)
			if err != nil {
				// Log error but continue - we'll use default title
				// Note: This error is expected if board_data table is empty
				boardData = nil
			}
			
			// Add board data to context even if nil
			ctx = context.WithValue(ctx, boardDataContextKey{}, boardData)
			
			// Continue with the request
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetBoardData retrieves board data from the request context
func GetBoardData(ctx context.Context) (interface{}, bool) {
	boardData := ctx.Value(boardDataContextKey{})
	return boardData, boardData != nil
}