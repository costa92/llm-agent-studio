import type { HttpParams, ScriptParams } from "@/lib/types"

// 从模板字符串中提取所有唯一 {{name}} 令牌名。
// 跨 systemPrompt + userPrompt 两个模板合并去重（Set 保序）。
// 恶意 {{ 无对应 }} 不崩溃（仅 match 完整 {{name}} 形式）。
export function extractTemplateVars(
  systemPrompt: string | undefined,
  userPrompt: string,
): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  const combined = (systemPrompt ?? "") + "\n" + userPrompt
  let m: RegExpExecArray | null
  while ((m = re.exec(combined)) !== null) {
    const name = m[1].trim()
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}

// http 类型的 {{name}} 令牌：跨 url + 所有 header 值 + bodyTemplate 合并去重。
// 排除 {{secret:NAME}} 引用（密钥不是工作流变量，不产生绑定行）。
export function extractHttpTemplateVars(params: HttpParams): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  const combined = [
    params.url,
    ...Object.values(params.headers ?? {}),
    params.bodyTemplate ?? "",
  ].join("\n")
  let m: RegExpExecArray | null
  while ((m = re.exec(combined)) !== null) {
    const name = m[1].trim()
    if (name.startsWith("secret:")) continue
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}

// script 类型的 {{name}} 令牌：从 code 模板提取去重（与 llm/http 一致）。
export function extractScriptTemplateVars(params: ScriptParams): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(params.code ?? "")) !== null) {
    const name = m[1].trim()
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}
