import type { JSONSchema7Reference } from "../index";
import type { DeserializationPattern } from "./deserializationPattern";
export declare type FromSchemaOptions = {
    parseNotKeyword?: boolean;
    parseIfThenElseKeywords?: boolean;
    references?: JSONSchema7Reference[] | false;
    deserialize?: DeserializationPattern[] | false;
};
export declare type FromSchemaDefaultOptions = {
    parseNotKeyword: false;
    parseIfThenElseKeywords: false;
    references: false;
    deserialize: false;
};
