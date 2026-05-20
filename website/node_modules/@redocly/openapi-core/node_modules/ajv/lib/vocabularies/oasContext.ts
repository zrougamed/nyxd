import type {AnySchemaObject} from "../types"

import {_, or, type Code} from "../compile/codegen"
import N from "../compile/names"

export function getSkipCondition(schema: AnySchemaObject, prop: string): Code | undefined {
  const propSchema = schema.properties?.[prop]
  if (!propSchema) return undefined

  const hasReadOnly = propSchema.readOnly === true
  const hasWriteOnly = propSchema.writeOnly === true

  if (!hasReadOnly && !hasWriteOnly) return undefined

  const conditions: Code[] = []
  const apiContext = _`typeof ${N.this} == "object" && ${N.this} && ${N.this}.apiContext`

  if (hasReadOnly) {
    conditions.push(_`${apiContext} === "request"`)
  }

  if (hasWriteOnly) {
    conditions.push(_`${apiContext} === "response"`)
  }

  return or(...conditions)
}
