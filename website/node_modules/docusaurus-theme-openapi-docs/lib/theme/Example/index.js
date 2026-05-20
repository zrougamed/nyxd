"use strict";
/* ============================================================================
 * Copyright (c) Palo Alto Networks
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 * ========================================================================== */
var __importDefault =
  (this && this.__importDefault) ||
  function (mod) {
    return mod && mod.__esModule ? mod : { default: mod };
  };
Object.defineProperty(exports, "__esModule", { value: true });
exports.renderExamplesRecord = exports.Example = void 0;
exports.renderStringArrayExamples = renderStringArrayExamples;
const react_1 = __importDefault(require("react"));
const Translate_1 = require("@docusaurus/Translate");
const SchemaTabs_1 = __importDefault(require("@theme/SchemaTabs"));
const TabItem_1 = __importDefault(require("@theme/TabItem"));
const translationIds_1 = require("@theme/translationIds");
const EXAMPLE_CLASS_NAME = "openapi-example";
const EXAMPLES_CLASS_NAME = "openapi-examples";
/**
 * Example Component
 */
const Example = ({ example, examples }) => {
  if (example !== undefined) {
    return renderExample(example);
  }
  if (examples !== undefined) {
    return renderExamples(examples);
  }
  return undefined;
};
exports.Example = Example;
/**
 * Format example value
 *
 * @param example
 * @returns
 */
const formatExample = (example) => {
  if (typeof example === "object" && example !== null) {
    return JSON.stringify(example);
  }
  return String(example);
};
const renderExample = (example) => {
  return react_1.default.createElement(
    "div",
    { className: EXAMPLE_CLASS_NAME },
    react_1.default.createElement(
      "strong",
      null,
      (0, Translate_1.translate)({
        id: translationIds_1.OPENAPI_SCHEMA_ITEM.EXAMPLE,
        message: "Example:",
      }),
      " "
    ),
    react_1.default.createElement(
      "span",
      null,
      react_1.default.createElement("code", null, formatExample(example))
    )
  );
};
const renderExamples = (examples) => {
  if (Array.isArray(examples)) {
    return renderStringArrayExamples(examples);
  }
  return (0, exports.renderExamplesRecord)(examples);
};
/**
 * Render string examples
 *
 * @param examples
 * @returns
 */
function renderStringArrayExamples(examples) {
  if (examples.length === 0) {
    return undefined;
  }
  // If there's only one example, display it without tabs
  if (examples.length === 1) {
    return renderExample(examples[0]);
  }
  // Multiple examples - use tabs
  const exampleEntries = examples.reduce(
    (acc, example, index) => ({
      ...acc,
      [`Example ${index + 1}`]: {
        value: example,
      },
    }),
    {}
  );
  return (0, exports.renderExamplesRecord)(exampleEntries);
}
const renderExamplesRecord = (examples) => {
  const exampleEntries = Object.entries(examples);
  // If there's only one example, display it without tabs
  if (exampleEntries.length === 1) {
    const firstExample = exampleEntries[0][1];
    if (!firstExample) {
      return undefined;
    }
    return renderExample(firstExample.value);
  }
  return react_1.default.createElement(
    "div",
    { className: EXAMPLES_CLASS_NAME },
    react_1.default.createElement(
      "strong",
      null,
      (0, Translate_1.translate)({
        id: translationIds_1.OPENAPI_SCHEMA_ITEM.EXAMPLES,
        message: "Examples:",
      })
    ),
    react_1.default.createElement(
      SchemaTabs_1.default,
      null,
      exampleEntries.map(([exampleName, exampleProperties]) =>
        renderExampleObject(exampleName, exampleProperties)
      )
    )
  );
};
exports.renderExamplesRecord = renderExamplesRecord;
/**
 * Render example object
 *
 * @param exampleName
 * @param exampleProperties
 * @returns
 */
const renderExampleObject = (exampleName, exampleProperties) => {
  return (
    // @ts-ignore
    react_1.default.createElement(
      TabItem_1.default,
      { value: exampleName, label: exampleName },
      exampleProperties.summary &&
        react_1.default.createElement("p", null, exampleProperties.summary),
      exampleProperties.description &&
        react_1.default.createElement(
          "p",
          null,
          react_1.default.createElement(
            "strong",
            null,
            (0, Translate_1.translate)({
              id: translationIds_1.OPENAPI_SCHEMA_ITEM.DESCRIPTION,
              message: "Description:",
            }),
            " "
          ),
          react_1.default.createElement(
            "span",
            null,
            exampleProperties.description
          )
        ),
      exampleProperties.value !== undefined
        ? renderExample(exampleProperties.value)
        : undefined
    )
  );
};
