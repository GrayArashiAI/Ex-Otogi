import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect, useRouter } from "@tanstack/react-router";
import { KeyRound } from "lucide-react";
import { z } from "zod";
import { api, data } from "@/api/client";
import { AuthCard } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { getPasskey } from "@/lib/passkey";
import { sessionQuery } from "@/queries/session";

export const Route = createFileRoute("/sign-in")({
  validateSearch: z.object({ next: z.string().optional() }),
  beforeLoad: async ({ context }) => {
    if ((await context.queryClient.fetchQuery(sessionQuery)).signed_in) throw redirect({ to: "/" });
  },
  component: SignIn,
});

function SignIn() {
  const { next } = Route.useSearch();
  const queryClient = useQueryClient();
  const router = useRouter();
  const signIn = useMutation({
    mutationFn: async () => {
      const options = await data(api.POST("/api/auth/login/begin"));
      const credential = await getPasskey(options);
      return data(api.POST("/api/auth/login/finish", { body: credential as never }));
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries(sessionQuery);
      await router.history.push(next?.startsWith("/") ? next : "/");
    },
  });
  return (
    <AuthCard title="Sign in">
      <p className="mt-1 text-muted-foreground text-sm">Use a passkey registered for this panel.</p>
      <Button className="mt-6 w-full" size="lg" loading={signIn.isPending} onClick={() => signIn.mutate()}>
        <KeyRound /> Sign in with a passkey
      </Button>
      <p className="mt-5 text-muted-foreground text-xs leading-relaxed">
        No passkey yet? On the server, run <code className="text-foreground">otogi passkey enroll</code> and open the
        link it prints.
      </p>
    </AuthCard>
  );
}
