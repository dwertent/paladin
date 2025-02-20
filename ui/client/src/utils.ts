// Copyright © 2025 Kaleido, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { IFilter } from "./interfaces";

export const formatJSONWhenApplicable = (value: any) => {
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value, null, 2);
    } catch (err) { }
  }
  return String(value);
};

export const translateFilters = (filters: IFilter[]) => {

  let result: any = {};

  for (const filter of filters) {

    let entry: any = {
      field: filter.field.name,
      value: filter.value,
    };

    if(filter.caseSensitive === false) {
      entry.caseInsensitive = true;
    }

    let operator = filter.operator;

    switch (operator) {
      case 'contains': operator = 'like'; entry.value = `%${entry.value}%`; break;
      case 'startsWith': operator = 'like'; entry.value = `${entry.value}%`; break;
      case 'endsWith': operator = 'like'; entry.value = `%${entry.value}`; break;
      case 'doesNotContain': operator = 'like'; entry.not = true; entry.value = `%${entry.value}%`; break;
      case 'doesNotStartWith': operator = 'like'; entry.not = true; entry.value = `${entry.value}%`; break;
      case 'doesNotEndWith': operator = 'like'; entry.not = true; entry.value = `%${entry.value}`; break;
    }

    let group = result[operator] ?? [];
    group.push(entry);
    result[operator] = group;
  }

  return result;

};
