import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

export const chatsQuery = queryOptions({
  queryKey: ["chats"],
  queryFn: async () => (await data(api.GET("/api/chats"))) ?? [],
  refetchInterval: 30_000,
});

export const chatQuery = (chat: string) =>
  queryOptions({
    queryKey: ["chats", chat],
    queryFn: () => data(api.GET("/api/chats/{chat}", { params: { path: { chat } } })),
  });

export const messagesQuery = (chat: string) =>
  infiniteQueryOptions({
    queryKey: ["chats", chat, "messages"],
    queryFn: ({ pageParam }) =>
      data(
        api.GET("/api/chats/{chat}/messages", { params: { path: { chat }, query: { before: pageParam, limit: 50 } } }),
      ),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (page) => (page.more ? page.messages?.at(-1)?.id : undefined),
    refetchInterval: 15_000,
  });
