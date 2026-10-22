package gmail

// The API types are generated from Google's Discovery document. Only the types
// come from there: request paths, authentication, retries and error mapping are
// hand-written in client.go, and the helpers over the types live in api.go.
//
// The document is downloaded on every run, so the header of the generated file
// carries the revision and SHA-256 it was built from. Regenerating therefore
// needs network access; the sandbox reaches it through HTTPS_PROXY.
//
//go:generate go run github.com/zhsj/yauzin/internal/cmd/gmailgen -o discovery_types.go -pkg gmail
