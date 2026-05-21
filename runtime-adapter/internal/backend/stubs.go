package backend

import (
	"context"
	"errors"
)

// LlamaCpp is a stub backend reserved for M-future when we integrate
// llama.cpp-server directly. Listed in the interface table so config
// files can name it now; calls fail until implemented.
type LlamaCpp struct{ BaseURL string }

func (b *LlamaCpp) Kind() Kind                                             { return KindLlamaCpp }
func (b *LlamaCpp) Health(context.Context) error                           { return ErrNotImplemented }
func (b *LlamaCpp) Capabilities(context.Context) (Capabilities, error)     { return Capabilities{Kind: KindLlamaCpp}, ErrNotImplemented }
func (b *LlamaCpp) Chat(context.Context, ChatRequest) (ChatResponse, error) { return ChatResponse{}, ErrNotImplemented }
func (b *LlamaCpp) ChatStream(context.Context, ChatRequest, StreamWriter) error { return ErrNotImplemented }

// LocalAI is a stub for the LocalAI backend; same status as LlamaCpp.
type LocalAI struct{ BaseURL string }

func (b *LocalAI) Kind() Kind                                                { return KindLocalAI }
func (b *LocalAI) Health(context.Context) error                              { return ErrNotImplemented }
func (b *LocalAI) Capabilities(context.Context) (Capabilities, error)        { return Capabilities{Kind: KindLocalAI}, ErrNotImplemented }
func (b *LocalAI) Chat(context.Context, ChatRequest) (ChatResponse, error)   { return ChatResponse{}, ErrNotImplemented }
func (b *LocalAI) ChatStream(context.Context, ChatRequest, StreamWriter) error { return ErrNotImplemented }

// RemoteAPI is a stub for the Anthropic/OpenAI/etc. bridge backend.
// Implementation lands when Clan members start advertising remote-API
// reasoning backends per PRD §6.2.
type RemoteAPI struct {
	Vendor   string // "anthropic" | "openai" | "openrouter" | "groq" | "custom-openai-compatible"
	BaseURL  string
	Model    string
	APIKeyID string // reference into the secret store, NOT the key itself
}

func (b *RemoteAPI) Kind() Kind                                                { return KindRemoteAPI }
func (b *RemoteAPI) Health(context.Context) error                              { return ErrNotImplemented }
func (b *RemoteAPI) Capabilities(context.Context) (Capabilities, error)        { return Capabilities{Kind: KindRemoteAPI, RemoteAPIVendor: b.Vendor}, ErrNotImplemented }
func (b *RemoteAPI) Chat(context.Context, ChatRequest) (ChatResponse, error)   { return ChatResponse{}, ErrNotImplemented }
func (b *RemoteAPI) ChatStream(context.Context, ChatRequest, StreamWriter) error { return ErrNotImplemented }

// ErrNotImplemented is returned by stub backends. Callers should surface it
// distinctly so the user knows the backend exists in config but isn't built.
var ErrNotImplemented = errors.New("backend not implemented in this build")
