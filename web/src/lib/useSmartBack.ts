import { useCanGoBack, useRouter } from "@tanstack/react-router"

// 顶栏「← 返回」的智能后退：优先真正后退到打开当前视图之前的那一页
// （运行列表 / 项目页 / 上一个视图，取决于来路），符合「←」箭头的直觉；
// 仅当没有应用内历史（深链直接打开）时才兜底执行 fallback（通常导航到项目页）。
//
// 配套约定：视图内的状态切换（编辑↔运行、选择 run、创建后跳转）应使用
// navigate({ replace: true })，不写入历史，否则后退会被这些内部切换困住、
// 回不到真正的上一步。
export function useSmartBack(fallback: () => void): () => void {
  const router = useRouter()
  const canGoBack = useCanGoBack()
  return () => {
    if (canGoBack) {
      router.history.back()
    } else {
      fallback()
    }
  }
}
