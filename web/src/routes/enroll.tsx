import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { KeyRound } from "lucide-react";
import { useState } from "react";
import { api, data } from "@/api/client";
import { AuthCard } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { createPasskey, passkeyName } from "@/lib/passkey";
import { sessionQuery } from "@/queries/session";

export const Route = createFileRoute("/enroll")({ component: Enroll });

// The link's token is in the fragment, which never reaches a server log.
function Enroll() {
  const token = window.location.hash.slice(1);
  const [name, setName] = useState(passkeyName);
  const queryClient = useQueryClient();
  const router = useRouter();
  const enroll = useMutation({
    mutationFn: async () => {
      const options = await data(api.POST("/api/auth/enroll/begin", { body: { token } }));
      const credential = await createPasskey(options);
      await data(api.POST("/api/auth/enroll/finish", { body: { name: name.trim(), credential } }));
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries(sessionQuery);
      await router.navigate({ to: "/" });
    },
  });
  if (!token) {
    return (
      <AuthCard title="This link is incomplete">
        <p className="mt-1 text-muted-foreground text-sm">
          Open the whole link <code className="text-foreground">otogi passkey enroll</code> printed, including the part
          after #.
        </p>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Register a passkey">
      <p className="mt-1 text-muted-foreground text-sm">It signs you in to this panel from now on.</p>
      <form
        className="mt-5 flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault();
          enroll.mutate();
        }}
      >
        <Field>
          <FieldLabel>Name</FieldLabel>
          <Input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
          <FieldDescription>To tell your passkeys apart, and to name who made each change.</FieldDescription>
        </Field>
        <Button type="submit" size="lg" loading={enroll.isPending} disabled={!name.trim()}>
          <KeyRound /> Register passkey
        </Button>
      </form>
    </AuthCard>
  );
}
