import { useQuery } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { CircleAlert, LogIn } from "lucide-react";
import { z } from "zod";
import { AuthCard } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { sessionQuery } from "@/queries/session";

export const Route = createFileRoute("/sign-in")({
  validateSearch: z.object({ next: z.string().optional(), error: z.string().optional() }),
  beforeLoad: async ({ context }) => {
    if ((await context.queryClient.fetchQuery(sessionQuery)).signed_in) throw redirect({ to: "/" });
  },
  component: SignIn,
});

// Signing in happens at the identity provider: the button leaves the panel
// for it, and it sends the browser back.
function SignIn() {
  const { next, error } = Route.useSearch();
  const { data: session } = useQuery(sessionQuery);
  const provider = session?.provider ? new URL(session.provider).host : "the identity provider";
  const back = next?.startsWith("/") ? next : "/";
  return (
    <AuthCard title="Sign in">
      <p className="mt-1 text-muted-foreground text-sm">With your {provider} account.</p>
      {error && (
        <p className="mt-4 flex gap-2 rounded-lg bg-destructive/6 px-3 py-2 text-destructive-foreground text-sm">
          <CircleAlert className="mt-0.5 size-4 flex-none" />
          {error}
        </p>
      )}
      <Button
        className="mt-6 w-full"
        size="lg"
        render={<a href={`/api/auth/login?next=${encodeURIComponent(back)}`} />}
      >
        <LogIn /> Sign in with {provider}
      </Button>
    </AuthCard>
  );
}
