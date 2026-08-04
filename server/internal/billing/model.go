package billing

import "time"

type ModelMetadata struct {
	InstanceID       string    `json:"instance_id"`
	ModelName        string    `json:"model_name"`
	MaxContextTokens int64     `json:"max_context_tokens"`
	Available        bool      `json:"available"`
	UpdatedAt        time.Time `json:"updated_at"`
	UpdatedBy        string    `json:"updated_by"`
}
