import type {Vocabulary} from "../types"

export const metadataVocabulary: Vocabulary = [
  "title",
  "description",
  "default",
  "deprecated",
  /**
   * readOnly/writeOnly are handled as validation keywords when OAS context is provided.
   * Keeping them here would register them as annotation-only metadata and would
   * prevent the context-aware validation behavior.
   *
   * @see ./validation/readOnly.ts
   * @see ./validation/writeOnly.ts
   */
  // "readOnly",
  // "writeOnly",
  "examples",
]

export const contentVocabulary: Vocabulary = [
  "contentMediaType",
  "contentEncoding",
  "contentSchema",
]
