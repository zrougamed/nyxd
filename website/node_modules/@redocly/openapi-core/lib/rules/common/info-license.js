import { missingRequiredField } from '../utils.js';
export const InfoLicense = () => {
    return {
        Info(info, { report }) {
            if (!info.license) {
                report({
                    message: missingRequiredField('Info', 'license'),
                    location: { reportOnKey: true },
                });
            }
        },
    };
};
//# sourceMappingURL=info-license.js.map