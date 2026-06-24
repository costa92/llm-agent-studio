import { describe, expect, it } from "vitest"
import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

// 守护唯一残留的重复：TYPE_LABEL 是同步画布渲染的标签源，后端目录也以这套标签
// 服务内置节点。两者偏离会让画布与「可添加」列表的标签不一致。
describe("nodeColor parity with backend builtin catalog", () => {
  it("TYPE_LABEL exactly matches the backend-served built-in labels", () => {
    expect(TYPE_LABEL).toEqual({ script: "剧本", storyboard: "分镜", asset: "资产", prescreen: "预审" })
  })

  it("NODE_COLOR has a color for every built-in type", () => {
    for (const type of ["script", "storyboard", "asset", "prescreen"]) {
      expect(NODE_COLOR[type]).toBeTruthy()
    }
  })
})
