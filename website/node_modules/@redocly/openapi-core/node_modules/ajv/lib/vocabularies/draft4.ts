import {Vocabulary} from "../types"
import refKeyword from "./core/ref"
import getApplicatorVocabulary from "./applicator"
import unevaluatedVocabulary from "./unevaluated"
import formatVocabulary from "./format"
import validationVocabulary from "./validation"
import limitNumber from "./validation/draft04/limitNumber"
import limitNumberExclusive from "./validation/draft04/limitNumberExclusive"

const metadataVocabulary: Vocabulary = ["title", "description", "default"]

const coreVocabulary: Vocabulary = [
  "$schema",
  "id",
  "$defs",
  {keyword: "$comment"},
  "definitions",
  refKeyword,
]

const validation: Vocabulary = [...validationVocabulary.slice(1), limitNumber, limitNumberExclusive]

const draft4Vocabularies: Vocabulary[] = [
  coreVocabulary,
  validation,
  getApplicatorVocabulary(),
  formatVocabulary,
  metadataVocabulary,
  unevaluatedVocabulary,
]

export default draft4Vocabularies
