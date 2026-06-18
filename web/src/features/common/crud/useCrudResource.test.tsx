import { describe, it, expect, vi } from "vitest"
import { renderHook, act, waitFor } from "@testing-library/react"
import { useCrudResource } from "./useCrudResource"

interface Row { id: string; name: string }
function setup(overrides: Partial<Parameters<typeof useCrudResource<Row>>[0]> = {}) {
  const create = vi.fn().mockResolvedValue(undefined)
  const update = vi.fn().mockResolvedValue(undefined)
  const remove = vi.fn().mockResolvedValue(undefined)
  const hook = renderHook(() =>
    useCrudResource<Row>({
      getId: (r) => r.id, create, update, remove,
      labels: { created: "已创建", updated: "已更新", deleted: "已删除" },
      ...overrides,
    }),
  )
  return { hook, create, update, remove }
}

describe("useCrudResource", () => {
  it("openCreate / openEdit / closeDialog 切换 dialog 状态", () => {
    const { hook } = setup()
    act(() => hook.result.current.openCreate())
    expect(hook.result.current.dialog).toEqual({ mode: "create", target: null })
    act(() => hook.result.current.openEdit({ id: "a", name: "A" }))
    expect(hook.result.current.dialog).toEqual({ mode: "edit", target: { id: "a", name: "A" } })
    act(() => hook.result.current.closeDialog())
    expect(hook.result.current.dialog).toBeNull()
  })

  it("submit 在 create 态调 create、edit 态调 update(id, values)，成功后关闭", async () => {
    const { hook, create, update } = setup()
    act(() => hook.result.current.openCreate())
    await act(async () => { hook.result.current.submit({ name: "新" }) })
    await waitFor(() => expect(create).toHaveBeenCalledWith({ name: "新" }))
    await waitFor(() => expect(hook.result.current.dialog).toBeNull())

    act(() => hook.result.current.openEdit({ id: "a", name: "A" }))
    await act(async () => { hook.result.current.submit({ name: "改" }) })
    await waitFor(() => expect(update).toHaveBeenCalledWith("a", { name: "改" }))
  })

  it("requestDelete/confirmDelete 调 remove(id) 并清空 deleteTarget", async () => {
    const { hook, remove } = setup()
    act(() => hook.result.current.requestDelete({ id: "a", name: "A" }))
    expect(hook.result.current.deleteTarget).toEqual({ id: "a", name: "A" })
    await act(async () => { hook.result.current.confirmDelete() })
    await waitFor(() => expect(remove).toHaveBeenCalledWith("a"))
    await waitFor(() => expect(hook.result.current.deleteTarget).toBeNull())
  })

  it("submit 失败时 submitError 经 errorMessage 映射、不关闭", async () => {
    const create = vi.fn().mockRejectedValue(new Error("boom"))
    const { hook } = setup({ create, errorMessage: () => "名称已存在" })
    act(() => hook.result.current.openCreate())
    await act(async () => { hook.result.current.submit({ name: "x" }) })
    await waitFor(() => expect(hook.result.current.submitError).toBe("名称已存在"))
    expect(hook.result.current.dialog).not.toBeNull()
  })
})
