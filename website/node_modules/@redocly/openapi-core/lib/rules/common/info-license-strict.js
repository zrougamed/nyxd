import { detectSpec } from '../../detect-spec.js';
import { validateDefinedAndNonEmpty, validateOneOfDefinedAndNonEmpty } from '../utils.js';
export const InfoLicenseStrict = () => {
    let specVersion;
    return {
        Root: {
            enter(root) {
                specVersion = detectSpec(root);
            },
            License: {
                leave(license, ctx) {
                    if (specVersion === 'oas3_1' || specVersion === 'oas3_2') {
                        validateOneOfDefinedAndNonEmpty(['url', 'identifier'], license, ctx);
                    }
                    else {
                        validateDefinedAndNonEmpty('url', license, ctx);
                    }
                },
            },
        },
    };
};
//# sourceMappingURL=info-license-strict.js.map