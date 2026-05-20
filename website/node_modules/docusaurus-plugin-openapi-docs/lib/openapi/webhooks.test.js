"use strict";
/* ============================================================================
 * Copyright (c) Palo Alto Networks
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 * ========================================================================== */
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const path_1 = __importDefault(require("path"));
// eslint-disable-next-line import/no-extraneous-dependencies
const utils_1 = require("@docusaurus/utils");
const _1 = require(".");
describe("webhooks", () => {
    it("uses event name when summary and operationId are missing", async () => {
        const files = await (0, _1.readOpenapiFiles)((0, utils_1.posixPath)(path_1.default.join(__dirname, "__fixtures__/webhook/openapi.yaml")));
        const [items] = await (0, _1.processOpenapiFiles)(files, { specPath: "", outputDir: "" }, {});
        const webhookItem = items.find((item) => item.type === "api");
        expect(webhookItem === null || webhookItem === void 0 ? void 0 : webhookItem.id).toBe("order-created");
    });
});
