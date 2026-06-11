import { createFileRoute } from "@tanstack/react-router"
import { PromptBuilder } from "@/features/prompt/PromptBuilder"
import { usePromptStyles, useBuildPrompt } from "@/features/prompt/api"

// T14：Prompt Builder（auth-only，风格选择 + build 预览）。
// 风格库与建项目/重生成共用；build 走 POST /api/prompt/build。
export const Route = createFileRoute("/_authed/orgs/$org/prompt")({
  component: PromptBuilderPage,
})

function PromptBuilderPage() {
  const styles = usePromptStyles()
  const build = useBuildPrompt()

  return (
    <PromptBuilder
      styles={styles.data}
      stylesLoading={styles.isLoading}
      onBuild={async (prompt, style) => {
        const res = await build.mutateAsync({ prompt, style })
        return res.prompt
      }}
    />
  )
}
