import React from "react";
import { ExampleObject } from "@theme/ParamsItem";
type ExampleType = string;
type ExamplesType = Record<string, ExampleObject> | string[];
/**
 * Example Component Props
 */
type ExampleProps = {
    example?: ExampleType;
    examples?: ExamplesType;
};
/**
 * Example Component
 */
export declare const Example: ({ example, examples }: ExampleProps) => React.JSX.Element | undefined;
/**
 * Render string examples
 *
 * @param examples
 * @returns
 */
export declare function renderStringArrayExamples(examples: string[]): React.JSX.Element | undefined;
export declare const renderExamplesRecord: (examples: Record<string, ExampleObject>) => React.JSX.Element | undefined;
export {};
