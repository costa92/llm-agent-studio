import { describe, it, expect } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { FormProvider, useForm } from "react-hook-form"
import { ProjectFields, type ProjectFieldsProps } from "./ProjectFields"
import { defaultsFor, type ProjectFormValues } from "./ProjectFields.schema"
import type { Style } from "@/lib/types"

const STYLES: Style[] = [
  { name: "写实", suffix: "" },
  { name: "动画", suffix: "" },
]

function Harness({
  initial,
  ...props
}: {
  initial?: Parameters<typeof defaultsFor>[0]
} & Partial<ProjectFieldsProps>) {
  const form = useForm<ProjectFormValues>({
    defaultValues: defaultsFor(initial),
  })
  return (
    <FormProvider {...form}>
      <ProjectFields styles={STYLES} fieldIdPrefix="t" {...props} />
    </FormProvider>
  )
}

describe("ProjectFields", () => {
  it("默认 standard 模式不渲染绘本配置", () => {
    render(<Harness />)
    expect(screen.getByLabelText("项目名称")).toBeInTheDocument()
    // 绘本配置的标志文案「年龄段」不出现。
    expect(screen.queryByText("年龄段")).not.toBeInTheDocument()
  })

  it("点「儿童绘本」切到 picturebook 模式并展开 PictureBookConfigForm", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(screen.getByRole("button", { name: "儿童绘本" }))
    // PictureBookConfigForm 经 Controller 渲染——其「年龄段」label 出现。
    expect(await screen.findByText("年龄段")).toBeInTheDocument()
  })

  it("initial 为 picturebook 时直接展开绘本配置并回填年龄段", () => {
    render(
      <Harness
        initial={{
          kind: "picturebook",
          pictureBookConfig: JSON.stringify({
            ageBand: "3-6",
            bookType: "narrative",
            illustrationStyle: "",
            narrationStyle: "plain",
            themes: [],
            pageCount: 16,
            voice: "",
          }),
        }}
      />,
    )
    expect(screen.getByText("年龄段")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "3-6" })).toBeInTheDocument()
  })

  it("alwaysShowPlanner 时即使无 textModels 也渲染规划下拉（保留 Edit 现状）", () => {
    render(<Harness alwaysShowPlanner project={{} as never} />)
    expect(screen.getByLabelText(/规划用模型/)).toBeInTheDocument()
  })

  it("不传 alwaysShowPlanner 且无 textModels 时不渲染规划下拉（保留 Create 现状）", () => {
    render(<Harness />)
    expect(screen.queryByLabelText(/规划用模型/)).not.toBeInTheDocument()
  })
})
