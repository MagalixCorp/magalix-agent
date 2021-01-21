package agent

import (
	"context"

	"github.com/open-policy-agent/frameworks/constraint/pkg/types"
)

type RecsHandler func(recommendations []*types.Result) error

type RecommendationsSource interface {
	Start(ctx context.Context) error
	Stop() error

	SetRecommendationHandler(handler RecsHandler)
}
