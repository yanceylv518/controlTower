package billing

import "context"

// QueuedGeneration identifies a whole job, including publication or all report
// dates. Only its site's lock owner may dispatch it.
type QueuedGeneration struct {
	Kind   string
	ID     string
	Status string
}

type GenerationQueueStore interface {
	GenerationSites(context.Context) ([]string, error)
	LockGenerationSite(context.Context, string) (context.Context, func(), error)
	NextGeneration(context.Context, string) (QueuedGeneration, error)
}
