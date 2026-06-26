import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import { TypeDialog } from "./TypeDialog"
import { emptyDraft } from "./typeDraft"

// TypeDialog 的 initialKind 种子能力（画布快建 chip 用）。
function renderDialog(props: Partial<React.ComponentProps<typeof TypeDialog>> = {}) {
  return render(
    <TypeDialog
      open
      mode="create"
      submitting={false}
      submitError={null}
      secretNames={[]}
      isAdmin
      onSubmit={vi.fn()}
      onOpenChange={vi.fn()}
      {...props}
    />,
  )
}

describe("TypeDialog initialKind", () => {
  it("defaults kind to llm when neither initial nor initialKind given", () => {
    renderDialog()
    expect(screen.getByLabelText("kind")).toHaveValue("llm")
  })

  it("seeds kind=http when initialKind='http'", () => {
    renderDialog({ initialKind: "http" })
    expect(screen.getByLabelText("kind")).toHaveValue("http")
    // http 表单字段出现（URL），llm 的用户提示词缺席。
    expect(screen.getByLabelText(/URL/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/用户提示词/)).not.toBeInTheDocument()
  })

  it("seeds kind=script when initialKind='script'", () => {
    renderDialog({ initialKind: "script" })
    expect(screen.getByLabelText("kind")).toHaveValue("script")
  })

  it("initial draft takes precedence over initialKind", () => {
    // 编辑/显式 initial 时忽略 initialKind。
    renderDialog({ initial: emptyDraft("llm"), initialKind: "http" })
    expect(screen.getByLabelText("kind")).toHaveValue("llm")
  })
})
