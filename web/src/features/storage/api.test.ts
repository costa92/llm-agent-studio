import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useGlobalStorageConfig,
  useUpsertGlobalStorageConfig,
} from "./api"
import { setAccessToken } from "@/lib/apiClient"
import type { StorageConfig } from "@/lib/types"

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

const GLOBAL: StorageConfig = {
  id: "sc-global-1",
  name: "global-default",
  scope: "global",
  orgId: "",
  mode: "s3",
  endpoint: "https://s3.amazonaws.com",
  region: "us-east-1",
  bucket: "global-bucket",
  accessKeyId: "AKIA",
  publicPrefix: "",
  useSsl: true,
  enabled: true,
  isDefault: true,
  hasSecret: true,
}

describe("global storage hooks → platform endpoint", () => {
  it("useGlobalStorageConfig GETs /api/platform/storage-config/global", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ config: GLOBAL }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useGlobalStorageConfig(), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual(GLOBAL)
    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/platform/storage-config/global",
    )
  })

  it("useUpsertGlobalStorageConfig PUTs /api/platform/storage-config/global", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(GLOBAL))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useUpsertGlobalStorageConfig(), {
      wrapper: wrapper(),
    })
    result.current.mutate({
      name: "global-default",
      mode: "s3",
      endpoint: "https://s3.amazonaws.com",
      region: "us-east-1",
      bucket: "global-bucket",
      accessKeyId: "AKIA",
      secret: "",
      useSsl: true,
      publicPrefix: "",
      enabled: true,
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/platform/storage-config/global",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("PUT")
  })
})
