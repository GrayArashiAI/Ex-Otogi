import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

/** EventFilter narrows event log records; empty fields do not filter. */
export type EventFilter = {
  kind?: string;
  component?: string;
  conversation?: string;
  outcome?: string;
  q?: string;
  since?: string;
};

export const eventsQuery = (filter: EventFilter) =>
  infiniteQueryOptions({
    queryKey: ["events", "list", filter],
    queryFn: ({ pageParam }) =>
      data(api.GET("/api/events", { params: { query: { ...filter, before: pageParam, limit: 200 } } })),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (page) => (page.more ? page.records.at(-1)?.id : undefined),
  });

/** newerQuery polls for records newer than after, for the live tail. */
export const newerQuery = (filter: EventFilter, after: number | undefined) =>
  queryOptions({
    queryKey: ["events", "newer", filter, after],
    queryFn: () => data(api.GET("/api/events", { params: { query: { ...filter, after, limit: 500 } } })),
    enabled: after !== undefined,
  });

export const failuresQuery = queryOptions({
  queryKey: ["events", "failures"],
  queryFn: () => data(api.GET("/api/events", { params: { query: { outcome: "error", limit: 8 } } })),
  refetchInterval: 15_000,
});

/** traceQuery is one event's records; it follows the event while it runs. */
export const traceQuery = (event: string) =>
  queryOptions({
    queryKey: ["events", "trace", event],
    queryFn: () => data(api.GET("/api/events/{id}/trace", { params: { path: { id: event } } })),
    refetchInterval: (q) => (q.state.data?.running.length ? 2000 : false),
  });

/** TaskFilter narrows tasks; empty fields do not filter. */
export type TaskFilter = {
  conversation?: string;
  outcome?: "running" | "error" | "slow" | "degraded" | "ok";
  q?: string;
};

/** tasksQuery pages through tasks, newest first; live refetches every few
 * seconds, as it should while any task runs. */
export const tasksQuery = (filter: TaskFilter, live: boolean) =>
  infiniteQueryOptions({
    queryKey: ["events", "tasks", filter],
    queryFn: ({ pageParam }) =>
      data(api.GET("/api/tasks", { params: { query: { ...filter, before: pageParam, limit: 50 } } })),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (page) => (page.more ? page.tasks.at(-1)?.cursor : undefined),
    refetchInterval: (q) => (live || q.state.data?.pages[0]?.tasks.some((t) => t.outcome === "running") ? 3000 : false),
  });
