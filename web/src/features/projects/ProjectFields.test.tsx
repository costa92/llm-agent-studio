import { describe, it, expect } from "vitest"
import { render, screen } from "@testing-library/react"
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
  it("渲染基本字段（名称等），不再渲染项目类型/绘本配置", () => {
    render(<Harness />)
    expect(screen.getByLabelText("项目名称")).toBeInTheDocument()
    // 工作流化后已移除项目类型切换与绘本配置。
    expect(screen.queryByText("项目类型")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "儿童绘本" })).not.toBeInTheDocument()
    expect(screen.queryByText("年龄段")).not.toBeInTheDocument()
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
