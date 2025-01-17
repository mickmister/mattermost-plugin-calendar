import {Action, Store} from 'redux';
import {GlobalState} from 'mattermost-redux/types/store';

import manifest from './manifest';

import './style.css';
import MainApp from 'app';

import {PluginRegistry} from './types/mattermost-webapp';

import reducer from 'reducers';

import {updateCalendarSettings} from './actions';
import {ApiClient} from './client';

const EmptyComponent = () => <></>;

export default class Plugin {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-function
    public async initialize(registry: PluginRegistry, store: Store<GlobalState, Action<Record<string, unknown>>>) {
        registry.registerReducer(reducer);
        registry.registerProduct(
            '/calendar',
            'calendar-outline',
            'Calendar',
            '/calendar',
            MainApp,
            EmptyComponent,
            EmptyComponent,
            true,
        );

        // Load calendar settings like playbooks
        const getCalendarSettings = async () => {
            store.dispatch(updateCalendarSettings(await ApiClient.getCalendarSettings()));
        };
        getCalendarSettings();
    }
}

declare global {
    interface Window {
        registerPlugin(id: string, plugin: Plugin): void
    }
}

window.registerPlugin(manifest.id, new Plugin());
