// TS mirror of internal/nodedesc/types.go. Wire keys must stay in lockstep — the
// Go↔TS parity test (nodeDesc.parity.test.ts) guards displayOptions/DefaultFrom.
export type PropertyType =
  | "string" | "textarea" | "number" | "boolean" | "options"
  | "collection" | "fixedCollection" | "json" | "code"
  | "prompt" | "keyValue" | "resourceLocator"

export interface OptionItem { value: string; label: string }

export interface DisplayOptions {
  show?: Record<string, unknown[]>
  hide?: Record<string, unknown[]>
}

export interface DerivedDefault {
  field: string
  map: Record<string, Record<string, unknown>>
}

export interface Constraints {
  noTemplate?: boolean
  noSecret?: boolean
  secretAllowedIn?: string[]
}

export interface TypeOptions {
  rows?: number
  editor?: string
  password?: boolean
  dataSource?: string
  promptKind?: string
}

export interface Property {
  name: string
  label: string
  type: PropertyType
  default?: unknown
  defaultFrom?: DerivedDefault
  required?: boolean
  options?: OptionItem[]
  displayOptions?: DisplayOptions
  typeOptions?: TypeOptions
  constraints?: Constraints
  placeholder?: string
}

export interface OutputField { name: string; type: string; desc?: string }
export interface PortSpec { name: string; type: string }

export interface NodeTypeDescription {
  type: string
  version: number
  label: string
  description: string
  group: string
  inputs: PortSpec[] | null
  outputs: PortSpec[] | null
  outputSchema?: OutputField[]
  properties: Property[]
}

export interface NodeTypesResponse {
  version: number
  nodeTypes: NodeTypeDescription[]
}
