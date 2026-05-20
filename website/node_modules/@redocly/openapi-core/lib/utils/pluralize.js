import pluralizeOne from 'pluralize';
export function pluralize(sentence, count, inclusive) {
    return sentence
        .split(' ')
        .map((word) => pluralizeOne(word, count, inclusive))
        .join(' ');
}
//# sourceMappingURL=pluralize.js.map