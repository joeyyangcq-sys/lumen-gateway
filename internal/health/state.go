package health

type State string

const (
	StateHealthy   State = "healthy"
	StateUnhealthy State = "unhealthy"
	StateHalfOpen  State = "half_open"
)
