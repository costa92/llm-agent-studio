import { describe, it, expect, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RevealSecretInput } from "./RevealSecretInput"

describe("RevealSecretInput", () => {
  it("默认 password 类型，点眼睛切到 text", async () => {
    render(<RevealSecretInput value="s3cr3t" onChange={() => {}} />)
    const input = screen.getByLabelText("密钥输入")
    expect(input).toHaveAttribute("type", "password")
    await userEvent.click(screen.getByRole("button", { name: "显示/隐藏密钥" }))
    expect(input).toHaveAttribute("type", "text")
  })

  it("alreadySet 显示「留空保持不变」提示", () => {
    render(<RevealSecretInput value="" onChange={() => {}} alreadySet />)
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()
  })

  it("onReveal 异步解密填入值", async () => {
    const onChange = vi.fn()
    const onReveal = vi.fn().mockResolvedValue("decrypted-key")
    render(<RevealSecretInput value="" onChange={onChange} alreadySet onReveal={onReveal} />)
    await userEvent.click(screen.getByRole("button", { name: "显示已存密钥" }))
    await waitFor(() => expect(onReveal).toHaveBeenCalled())
    await waitFor(() => expect(onChange).toHaveBeenCalledWith("decrypted-key"))
  })

  it("无 onReveal 时不渲染「显示已存密钥」按钮", () => {
    render(<RevealSecretInput value="" onChange={() => {}} alreadySet />)
    expect(screen.queryByRole("button", { name: "显示已存密钥" })).not.toBeInTheDocument()
  })
})
