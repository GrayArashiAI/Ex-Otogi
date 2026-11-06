import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];

/** The management API, typed from its OpenAPI document (pnpm gen:api). */
export const api = createClient<paths>({ credentials: "same-origin" });

/** ApiError is a failed request, with the RFC 9457 problem the API sent. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Schemas["ErrorModel"] | undefined;
  constructor(status: number, problem: Schemas["ErrorModel"] | undefined) {
    super(problem?.detail ?? problem?.title ?? `The server answered ${status}`);
    this.status = status;
    this.problem = problem;
  }
}

/** data returns a response's body, or throws an ApiError. */
export async function data<T>(request: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await request;
  if (!response.ok) {
    throw new ApiError(response.status, error as Schemas["ErrorModel"] | undefined);
  }
  return data as T;
}
