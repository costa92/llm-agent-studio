import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { useTaskBoard } from "./api"
import { setAccessToken } from "@/lib/apiClient"
import type { TaskBoardResponse } from "@/lib/types"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

describe("useTaskBoard", () => {
  it("requests GET /api/orgs/{org}/tasks and returns the envelope", async () => {
    const board: TaskBoardResponse = {
      items: [],
      counts: { all: 0, running: 0, review: 0, failed: 0, completed: 0, draft: 0 },
    }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(board))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useTaskBoard("acme"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/orgs/acme/tasks")
    expect(result.current.data?.counts.all).toBe(0)
  })

  it("does not fire when org is empty", () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)

    renderHook(() => useTaskBoard(""), { wrapper: wrapper() })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
