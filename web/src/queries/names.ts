import { useQuery } from "@tanstack/react-query";
import { chatName } from "@/lib/chats";
import { chatsQuery } from "./chats";

/** useChatNames names records' chats: the directory's name for a chat the
 * bot has seen, else its full ID. */
export function useChatNames() {
  const { data: chats = [] } = useQuery(chatsQuery);
  const names = new Map(chats.map((c) => [c.id, chatName(c)]));
  return (platform?: string, conversation?: string) => {
    if (!platform || !conversation) return "";
    const id = `${platform}:${conversation}`;
    return names.get(id) ?? id;
  };
}
