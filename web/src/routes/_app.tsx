import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet, redirect, useRouter, useRouterState } from "@tanstack/react-router";
import { ChevronDown, KeyRound, LogOut } from "lucide-react";
import { api, data } from "@/api/client";
import { Search } from "@/components/search";
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
  { to: "/status", label: "Status" },
  { to: "/modules", label: "Modules" },
  { to: "/chats", label: "Chats" },
  { to: "/events", label: "Event log" },
] as const;

function Shell() {
  return (
    <>
      <header className="sticky top-0 z-20 flex h-14 items-center gap-7 border-b bg-background/88 px-4 backdrop-blur-md sm:px-7">
        <Link to="/" className="font-bold text-lg tracking-[-0.04em]">
          otogi
        </Link>
        <nav className="hidden gap-1 sm:flex">
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
        <PhoneNav />
        <Search />
        <Account />
      </header>
      <Outlet />
    </>
  );
}

/** PhoneNav is the navigation folded into a menu on narrow screens. */
function PhoneNav() {
  const here = useRouterState({ select: (s) => s.location.pathname });
  const current = nav.find((item) => here.startsWith(item.to));
  return (
    <Menu>
      <MenuTrigger className="-ml-3 flex items-center gap-1 rounded-lg px-2 py-1.5 font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring sm:hidden">
        {current?.label ?? "Menu"} <ChevronDown className="size-4 text-muted-foreground" />
      </MenuTrigger>
      <MenuPopup align="start">
        {nav.map((item) => (
          <MenuLinkItem key={item.to} render={<Link to={item.to} />}>
            {item.label}
          </MenuLinkItem>
        ))}
      </MenuPopup>
    </Menu>
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
        className="ml-auto grid size-8 place-items-center rounded-full md:ml-0 bg-linear-to-b from-neutral-700 to-neutral-900 font-semibold text-white text-xs shadow-xs inset-shadow-[0_1px_--theme(--color-white/20%)] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
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
