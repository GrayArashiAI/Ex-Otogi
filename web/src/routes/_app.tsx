import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet, redirect, useRouter } from "@tanstack/react-router";
import { KeyRound, LogOut } from "lucide-react";
import { api, data } from "@/api/client";
import { Menu, MenuItem, MenuLinkItem, MenuPopup, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { sessionQuery } from "@/queries/session";

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    const session = await context.queryClient.ensureQueryData(sessionQuery);
    if (!session.signed_in) {
      throw redirect({ to: "/sign-in", search: { next: location.href } });
    }
  },
  component: Shell,
});

const nav = [
  { to: "/modules", label: "Modules" },
  { to: "/chats", label: "Chats" },
] as const;

function Shell() {
  return (
    <>
      <header className="sticky top-0 z-20 flex h-14 items-center gap-7 border-b bg-background/88 px-4 backdrop-blur-md sm:px-7">
        <Link to="/" className="font-bold text-lg tracking-[-0.04em]">
          otogi
        </Link>
        <nav className="flex gap-1">
          {nav.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className="rounded-lg px-2.5 py-1.5 text-muted-foreground text-sm transition-colors hover:text-foreground data-[status=active]:bg-accent data-[status=active]:font-medium data-[status=active]:text-foreground"
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <Account />
      </header>
      <Outlet />
    </>
  );
}

function Account() {
  const { data: session } = useQuery(sessionQuery);
  const queryClient = useQueryClient();
  const router = useRouter();
  const signOut = useMutation({
    mutationFn: () => data(api.POST("/api/auth/logout")),
    onSuccess: async () => {
      queryClient.clear();
      await router.navigate({ to: "/sign-in" });
    },
  });
  const name = session?.passkey ?? "";
  return (
    <Menu>
      <MenuTrigger
        className="ml-auto grid size-8 place-items-center rounded-full bg-linear-to-b from-neutral-700 to-neutral-900 font-semibold text-white text-xs shadow-xs inset-shadow-[0_1px_--theme(--color-white/20%)] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
        aria-label="Account"
      >
        {name.slice(0, 1).toUpperCase() || "?"}
      </MenuTrigger>
      <MenuPopup align="end">
        <div className="px-2 py-1.5 text-muted-foreground text-xs">Signed in with {name}</div>
        <MenuSeparator />
        <MenuLinkItem render={<Link to="/passkeys" />}>
          <KeyRound /> Passkeys
        </MenuLinkItem>
        <MenuItem onClick={() => signOut.mutate()}>
          <LogOut /> Sign out
        </MenuItem>
      </MenuPopup>
    </Menu>
  );
}
