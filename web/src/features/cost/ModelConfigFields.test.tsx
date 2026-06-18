import { describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { FormProvider, useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import type { CatalogEntry, ModelConfig } from "@/lib/types"
import type { ListModelsInput, ListModelsResult } from "./api"
import {
  ModelConfigFields,
  defaultsFor,
  parseParamsText,
  providersFor,
  formSchema,
  type FormValues,
} from "./ModelConfigFields"

const CATALOG: CatalogEntry[] = [
  { provider: "openai", model: "gpt-image-1", kind: "image", label: "OpenAI GPT-Image-1", available: true },
  { provider: "openai", model: "gpt-4o-mini", kind: "text", label: "OpenAI GPT-4o-mini", available: true },
  { provider: "deepseek", model: "deepseek-chat", kind: "text", label: "DeepSeek Chat", available: true },
]

// 测试宿主：rhf FormProvider 包住 ModelConfigFields，附一个「保存」按钮触发 zod 校验。
function Host({
  initial,
  onListModels,
  onRevealKey,
  onValid,
}: {
  initial?: ModelConfig
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  onRevealKey?: (id: string) => Promise<string>
  onValid?: (v: FormValues) => void
}) {
  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema) as never,
    defaultValues: defaultsFor(initial, providersFor(CATALOG)),
  })
  return (
    <FormProvider {...form}>
      <form onSubmit={form.handleSubmit((v) => onValid?.(v))} noValidate>
        <ModelConfigFields
          catalog={CATALOG}
          initial={initial}
          onListModels={onListModels}
          onRevealKey={onRevealKey}
        />
        <button type="submit">保存</button>
      </form>
    </FormProvider>
  )
}

describe("parseParamsText", () => {
  it("returns undefined for empty / whitespace", () => {
    expect(parseParamsText("")).toBeUndefined()
    expect(parseParamsText("   ")).toBeUndefined()
  })

  it("parses a JSON object", () => {
    expect(parseParamsText('{"size":"1024x1024"}')).toEqual({ size: "1024x1024" })
  })

  it("throws on invalid JSON", () => {
    expect(() => parseParamsText("{not json")).toThrow("参数不是合法 JSON")
  })

  it("throws when JSON is not an object", () => {
    expect(() => parseParamsText("[1,2]")).toThrow("参数必须是 JSON 对象")
    expect(() => parseParamsText('"a string"')).toThrow("参数必须是 JSON 对象")
  })
})

describe("ModelConfigFields", () => {
  it("requires Base URL for the openai-compatible provider", async () => {
    const onValid = vi.fn()
    const user = userEvent.setup()
    render(<Host onValid={onValid} />)

    await user.selectOptions(screen.getByLabelText("Provider"), "openai-compatible")
    await user.type(screen.getByLabelText("模型 (model)"), "deepseek-chat")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() =>
      expect(screen.getByText(/请填写 Base URL/)).toBeInTheDocument(),
    )
    expect(onValid).not.toHaveBeenCalled()
  })

  it("拉取模型 populates model suggestions and fills input on chip click", async () => {
    const onListModels = vi
      .fn()
      .mockResolvedValue({ models: ["gpt-4o", "o3-mini"], source: "live" })
    const user = userEvent.setup()
    render(<Host onListModels={onListModels} />)

    await user.click(screen.getByRole("button", { name: /拉取模型/ }))
    await waitFor(() => expect(onListModels).toHaveBeenCalledTimes(1))
    // request carries the selected provider (default = first catalog provider).
    expect(onListModels.mock.calls[0][0]).toMatchObject({ provider: "openai" })

    expect(await screen.findByText(/已从官方接口拉取 2 个模型/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "gpt-4o" }))
    expect(screen.getByLabelText("模型 (model)")).toHaveValue("gpt-4o")
  })

  it("hides the 拉取模型 button when onListModels is absent", () => {
    render(<Host />)
    expect(
      screen.queryByRole("button", { name: /拉取模型/ }),
    ).not.toBeInTheDocument()
  })
})
