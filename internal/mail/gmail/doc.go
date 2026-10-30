// Package gmail implements mail.Service on top of the Gmail REST API: reading
// folders, conversations and message sources, and the mailbox changes the list
// and the reader make (flags, archive, trash). It never imports the UI or Gio,
// so it can be tested as a plain Go package.
//
// The account is authorized with a user-supplied OAuth client (see SignIn) and
// the gmail.modify scope, which covers the label changes behind those actions
// but not permanent deletion.
//
// The API types are generated from Google's Discovery document. Only the types
// come from there: request paths, authentication, retries and error mapping are
// hand-written in client.go, and the helpers over the types live in message.go.
//
// The document is downloaded on every run, so the header of the generated file
// carries the revision and SHA-256 it was built from. Regenerating therefore
// needs network access; the sandbox reaches it through HTTPS_PROXY.
package gmail

//go:generate go run github.com/zhsj/yauzin/internal/cmd/gmailgen -o discovery_types.go -pkg gmail
