module github.com/nebelhaus/holt

go 1.26

// Deliberately dependency-free through 0.1. `go build` works offline, CI needs
// no module proxy, and the binary stays small. The UX layer (fang/lipgloss) and
// the TOML adapter parser arrive in 0.2, when there is something to style.
