declare module 'leaflet-geosearch' {
    import { Control, ControlOptions } from 'leaflet';

    export interface SearchControlProps extends ControlOptions {
        provider: any;
        style?: 'bar' | 'button';
        showMarker?: boolean;
        showPopup?: boolean;
        autoClose?: boolean;
        retainZoomLevel?: boolean;
        animateZoom?: boolean;
        keepResult?: boolean;
        searchLabel?: string;
    }

    export class GeoSearchControl extends Control {
        constructor(options: SearchControlProps);
    }

    export class OpenStreetMapProvider {
        constructor(options?: any);
        search(options: { query: string }): Promise<any[]>;
    }
}
