import { missingRequiredField } from '../utils.js';
export const InfoContact = () => {
    return {
        Info(info, { report, location }) {
            if (!info.contact) {
                report({
                    message: missingRequiredField('Info', 'contact'),
                    location: location.child('contact').key(),
                });
            }
        },
    };
};
//# sourceMappingURL=info-contact.js.map