package api

func ptr[T any](value T) *T { return &value }

func value[T any](pointer *T) T {
	var zero T
	if pointer == nil {
		return zero
	}
	return *pointer
}
