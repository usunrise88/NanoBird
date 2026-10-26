package tools

import (
	"context"
	"testing"
)

func TestAllowAlwaysCoversTheFamilyForTheSession(t *testing.T) {
	asked := 0
	env := &Env{Permit: func(context.Context, Permission) Answer {
		asked++
		return AllowAlways
	}}
	ctx := context.Background()
	npm := Permission{Tool: "run_shell", Command: "npm install x", Family: "npm"}
	if !env.permit(ctx, npm) {
		t.Fatal("an allowed family was refused")
	}
	if !env.permit(ctx, npm) {
		t.Fatal("the remembered family was refused")
	}
	if asked != 1 {
		t.Fatalf("the client was asked %d times for one family, want 1", asked)
	}
	if !env.permit(ctx, Permission{Tool: "run_shell", Command: "git pull", Family: "git"}) || asked != 2 {
		t.Fatalf("another family did not ask: asked = %d", asked)
	}
}

func TestWithoutAClientAPermissionIsDenied(t *testing.T) {
	env := &Env{}
	if env.permit(context.Background(), Permission{Tool: "run_shell", Family: "npm"}) {
		t.Fatal("a run with no client to ask allowed a call")
	}
}

func TestAllowOnceDoesNotRememberTheFamily(t *testing.T) {
	asked := 0
	env := &Env{Permit: func(context.Context, Permission) Answer {
		asked++
		return AllowOnce
	}}
	p := Permission{Tool: "run_shell", Command: "npm install x", Family: "npm"}
	env.permit(context.Background(), p)
	env.permit(context.Background(), p)
	if asked != 2 {
		t.Fatalf("allow once was remembered: asked = %d", asked)
	}
}

func TestFamilyIsTheFirstWordOfTheCommand(t *testing.T) {
	if got := family("  npm   install x "); got != "npm" {
		t.Fatalf("family = %q", got)
	}
	if got := family(""); got != "" {
		t.Fatalf("family of nothing = %q", got)
	}
}
