import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { ChevronDown, LogOut } from "lucide-react";
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
      <a
        href="#content"
        className="sr-only z-30 rounded-lg bg-background px-3 py-2 font-medium text-sm shadow-md/10 outline-none ring-2 ring-ring focus:not-sr-only focus:absolute focus:top-2 focus:left-2"
      >
        Skip to content
      </a>
      <header className="z-20 flex h-14 flex-none items-center gap-7 border-b bg-background px-4 sm:px-7">
        <Link
          to="/"
          className="hit-target relative rounded-md font-bold text-lg tracking-[-0.04em] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        >
          otogi
        </Link>
        <nav className="hidden gap-1 sm:flex">
          {nav.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className="rounded-lg px-2.5 py-1.5 text-muted-foreground text-sm outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-[status=active]:bg-accent data-[status=active]:font-medium data-[status=active]:text-foreground"
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <PhoneNav />
        <div className="ml-auto flex items-center gap-3 md:gap-7">
          <Search />
          <Account />
        </div>
      </header>
      {/* The one scroll region under the header: pages either scroll in it
          or fill it and scroll their own lists. */}
      <main
        id="content"
        tabIndex={-1}
        data-scroll-restoration-id="content"
        className="flex min-h-0 flex-1 flex-col overflow-y-auto outline-none"
      >
        <Outlet />
      </main>
    </>
  );
}

/** PhoneNav is the navigation folded into a menu on narrow screens. */
function PhoneNav() {
  const here = useRouterState({ select: (s) => s.location.pathname });
  const current = nav.find((item) => here.startsWith(item.to));
  return (
    <Menu>
      <MenuTrigger className="hit-target relative -ml-3 flex items-center gap-1 rounded-lg px-2 py-1.5 font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring sm:hidden">
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
  const signOut = useMutation({
    mutationFn: () => data(api.POST("/api/auth/logout")),
    // Signing out ends the provider's session too, which happens there.
    onSuccess: (out) => {
      queryClient.clear();
      window.location.assign(out.next);
    },
  });
  const name = session?.name || session?.username || "";
  return (
    <Menu>
      <MenuTrigger
        className="hit-target relative grid size-8 place-items-center rounded-full bg-linear-to-b from-neutral-700 to-neutral-900 font-semibold text-white text-xs shadow-xs inset-shadow-[0_1px_--theme(--color-white/20%)] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
        aria-label="Account"
      >
        {name.slice(0, 1).toUpperCase() || "?"}
      </MenuTrigger>
      <MenuPopup align="end">
        <div className="px-2 py-1.5 text-muted-foreground text-xs">
          Signed in as {name}
          {session?.username && session.name && session.username !== session.name && (
            <span className="block font-mono">{session.username}</span>
          )}
        </div>
        <MenuSeparator />
        <MenuItem onClick={() => signOut.mutate()}>
          <LogOut /> Sign out
        </MenuItem>
      </MenuPopup>
    </Menu>
  );
}
