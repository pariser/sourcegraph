import { percySnapshot as realPercySnapshot } from '@percy/puppeteer'
import * as jsonc from '@sqs/jsonc-parser'
import * as jsoncEdit from '@sqs/jsonc-parser/lib/edit'
import * as os from 'os'
import puppeteer, { LaunchOptions, PageEventObj, Page, Serializable } from 'puppeteer'
import { Key } from 'ts-key-enum'
import * as util from 'util'
import { dataOrThrowErrors, gql, GraphQLResult } from '../graphql/graphql'
import { IMutation, IQuery } from '../graphql/schema'
import { readEnvBoolean, readEnvString, retry } from './e2e-test-utils'

/**
 * Returns a Promise for the next emission of the given event on the given Puppeteer page.
 */
export const oncePageEvent = <E extends keyof PageEventObj>(page: Page, eventName: E): Promise<PageEventObj[E]> =>
    new Promise(resolve => page.once(eventName, resolve))

export const percySnapshot = readEnvBoolean({ variable: 'PERCY_ON', defaultValue: false })
    ? realPercySnapshot
    : () => Promise.resolve()

/**
 * Used in the external service configuration.
 */
export const gitHubToken = readEnvString({ variable: 'GITHUB_TOKEN' })

export const sourcegraphBaseUrl = readEnvString({
    variable: 'SOURCEGRAPH_BASE_URL',
    defaultValue: 'http://localhost:3080',
})

/**
 * Specifies how to select the content of the element. No
 * single method works in all cases:
 *
 * - Meta+A doesn't work in input boxes https://github.com/GoogleChrome/puppeteer/issues/1313
 * - selectall doesn't work in the Monaco editor
 */
type SelectTextMethod = 'selectall' | 'keyboard'

/**
 * Specifies how to enter text. Typing is preferred in cases where it's important to test
 * the process of manually typing out the text to enter. Pasting is preferred in cases
 * where typing would be too slow or we explicitly want to test paste behavior.
 */
type EnterTextMethod = 'type' | 'paste'

export class Driver {
    constructor(public browser: puppeteer.Browser, public page: puppeteer.Page) {}

    public async ensureLoggedIn(): Promise<void> {
        await this.page.goto(sourcegraphBaseUrl)
        await this.page.evaluate(() => {
            localStorage.setItem('has-dismissed-browser-ext-toast', 'true')
            localStorage.setItem('has-dismissed-integrations-toast', 'true')
            localStorage.setItem('has-dismissed-survey-toast', 'true')
        })
        const url = new URL(this.page.url())
        if (url.pathname === '/site-admin/init') {
            await this.page.type('input[name=email]', 'test@test.com')
            await this.page.type('input[name=username]', 'test')
            await this.page.type('input[name=password]', 'test')
            await this.page.click('button[type=submit]')
            await this.page.waitForNavigation()
        } else if (url.pathname === '/sign-in') {
            await this.page.type('input', 'test')
            await this.page.type('input[name=password]', 'test')
            await this.page.click('button[type=submit]')
            await this.page.waitForNavigation()
        }
    }

    public async close(): Promise<void> {
        await this.browser.close()
    }

    public async newPage(): Promise<void> {
        this.page = await this.browser.newPage()
    }

    public async selectAll(method: SelectTextMethod = 'selectall'): Promise<void> {
        switch (method) {
            case 'selectall': {
                await this.page.evaluate(() => document.execCommand('selectall', false))
                break
            }
            case 'keyboard': {
                const modifier = os.platform() === 'darwin' ? Key.Meta : Key.Control
                await this.page.keyboard.down(modifier)
                await this.page.keyboard.press('a')
                await this.page.keyboard.up(modifier)
                break
            }
        }
    }

    public async enterText(method: EnterTextMethod = 'type', text: string): Promise<void> {
        // Pasting does not work on macOS. See:  https://github.com/GoogleChrome/puppeteer/issues/1313
        method = os.platform() === 'darwin' ? 'type' : method
        switch (method) {
            case 'type':
                await this.page.keyboard.type(text)
                break
            case 'paste':
                await this.paste(text)
                break
        }
    }

    public async replaceText({
        selector,
        newText,
        selectMethod = 'selectall',
        enterTextMethod = 'type',
    }: {
        selector: string
        newText: string
        selectMethod?: SelectTextMethod
        enterTextMethod?: EnterTextMethod
    }): Promise<void> {
        // The Monaco editor sometimes detaches nodes from the DOM, causing
        // `click()` to fail unpredictably.
        await retry(async () => {
            await this.page.waitForSelector(selector)
            await this.page.click(selector)
        })
        await this.selectAll(selectMethod)
        await this.page.keyboard.press(Key.Backspace)
        await this.enterText(enterTextMethod, newText)
    }

    public async acceptNextDialog(): Promise<void> {
        const dialog = await oncePageEvent(this.page, 'dialog')
        await dialog.accept()
    }

    public async ensureHasExternalService({
        kind,
        displayName,
        config,
        ensureRepos,
    }: {
        kind: string
        displayName: string
        config: string
        ensureRepos?: string[]
    }): Promise<void> {
        await this.page.goto(sourcegraphBaseUrl + '/site-admin/external-services')
        await this.page.waitFor('.e2e-filtered-connection')
        await this.page.waitForSelector('.e2e-filtered-connection__loader', { hidden: true })

        // Matches buttons for deleting external services named ${displayName}.
        const deleteButtonSelector = `[data-e2e-external-service-name="${displayName}"] .e2e-delete-external-service-button`
        if (await this.page.$(deleteButtonSelector)) {
            await Promise.all([this.acceptNextDialog(), this.page.click(deleteButtonSelector)])
        }

        await (await this.page.waitForSelector('.e2e-goto-add-external-service-page', { visible: true })).click()

        await (await this.page.waitForSelector(`[data-e2e-external-service-card-link="${kind.toUpperCase()}"]`, {
            visible: true,
        })).click()

        await this.replaceText({
            selector: '#e2e-external-service-form-display-name',
            newText: displayName,
        })

        // Type in a new external service configuration.
        await this.replaceText({
            selector: '.view-line',
            newText: config,
            selectMethod: 'keyboard',
        })
        await this.page.click('.e2e-add-external-service-button')
        await this.page.waitForNavigation()

        if (ensureRepos) {
            // Clone the repositories
            for (const slug of ensureRepos) {
                await this.page.goto(sourcegraphBaseUrl + `/site-admin/repositories?query=${encodeURIComponent(slug)}`)
                await this.page.waitForSelector(`.repository-node[data-e2e-repository='${slug}']`, {
                    visible: true,
                })
            }
        }
    }

    public async paste(value: string): Promise<void> {
        await this.page.evaluate(
            async d => {
                await navigator.clipboard.writeText(d.value)
            },
            { value }
        )
        const modifier = os.platform() === 'darwin' ? Key.Meta : Key.Control
        await this.page.keyboard.down(modifier)
        await this.page.keyboard.press('v')
        await this.page.keyboard.up(modifier)
    }

    public async assertWindowLocation(location: string, isAbsolute = false): Promise<any> {
        const url = isAbsolute ? location : sourcegraphBaseUrl + location
        await retry(async () => {
            expect(await this.page.evaluate(() => window.location.href)).toEqual(url)
        })
    }

    public async assertWindowLocationPrefix(locationPrefix: string, isAbsolute = false): Promise<any> {
        const prefix = isAbsolute ? locationPrefix : sourcegraphBaseUrl + locationPrefix
        await retry(async () => {
            const loc: string = await this.page.evaluate(() => window.location.href)
            expect(loc.startsWith(prefix)).toBeTruthy()
        })
    }

    public async assertStickyHighlightedToken(label: string): Promise<void> {
        await this.page.waitForSelector('.selection-highlight-sticky', { visible: true }) // make sure matched token is highlighted
        await retry(async () =>
            expect(
                await this.page.evaluate(() => document.querySelector('.selection-highlight-sticky')!.textContent)
            ).toEqual(label)
        )
    }

    public async assertAllHighlightedTokens(label: string): Promise<void> {
        const highlightedTokens = await this.page.evaluate(() =>
            Array.from(document.querySelectorAll('.selection-highlight')).map(el => el.textContent || '')
        )
        expect(highlightedTokens.every(txt => txt === label)).toBeTruthy()
    }

    public async assertNonemptyLocalRefs(): Promise<void> {
        // verify active group is references
        await this.page.waitForXPath(
            "//*[contains(@class, 'panel__tabs')]//*[contains(@class, 'tab-bar__tab--active') and contains(text(), 'References')]"
        )
        // verify there are some references
        await this.page.waitForSelector('.panel__tabs-content .file-match-children__item', { visible: true })
    }

    public async assertNonemptyExternalRefs(): Promise<void> {
        // verify active group is references
        await this.page.waitForXPath(
            "//*[contains(@class, 'panel__tabs')]//*[contains(@class, 'tab-bar__tab--active') and contains(text(), 'References')]"
        )
        // verify there are some references
        await this.page.waitForSelector('.panel__tabs-content .hierarchical-locations-view__item', { visible: true })
    }

    private async makeRequest<T = void>({ url, init }: { url: string; init: RequestInit & Serializable }): Promise<T> {
        const handle = await this.page.evaluateHandle((url, init) => fetch(url, init).then(r => r.json()), url, init)
        return handle.jsonValue()
    }

    private async makeGraphQLRequest<T extends IQuery | IMutation>({
        request,
        variables,
    }: {
        request: string
        variables: {}
    }): Promise<GraphQLResult<T>> {
        const nameMatch = request.match(/^\s*(?:query|mutation)\s+(\w+)/)
        const xhrHeaders = await this.page.evaluate(() => (window as any).context.xhrHeaders)
        const response = await this.makeRequest<GraphQLResult<T>>({
            url: `${sourcegraphBaseUrl}/.api/graphql${nameMatch ? '?' + nameMatch[1] : ''}`,
            init: {
                method: 'POST',
                body: JSON.stringify({ query: request, variables }),
                headers: {
                    ...xhrHeaders,
                    Accept: 'application/json',
                    'Content-Type': 'application/json',
                },
            },
        })
        return response
    }

    public async ensureHasCORSOrigin({ corsOriginURL }: { corsOriginURL: string }): Promise<void> {
        const currentConfigResponse = await this.makeGraphQLRequest<IQuery>({
            request: gql`
                query Site {
                    site {
                        id
                        configuration {
                            id
                            effectiveContents
                            validationMessages
                        }
                    }
                }
            `,
            variables: {},
        })
        const { site } = dataOrThrowErrors(currentConfigResponse)
        const currentConfig = site.configuration.effectiveContents
        const newConfig = modifyJSONC(currentConfig, ['corsOrigin'], oldCorsOrigin => {
            const urls = oldCorsOrigin ? oldCorsOrigin.value.split(' ') : []
            return (urls.includes(corsOriginURL) ? urls : [...urls, corsOriginURL]).join(' ')
        })
        const updateConfigResponse = await this.makeGraphQLRequest<IMutation>({
            request: gql`
                mutation UpdateSiteConfiguration($lastID: Int!, $input: String!) {
                    updateSiteConfiguration(lastID: $lastID, input: $input)
                }
            `,
            variables: { lastID: site.configuration.id, input: newConfig },
        })
        dataOrThrowErrors(updateConfigResponse)
    }

    public async resetUserSettings(): Promise<void> {
        const currentSettingsResponse = await this.makeGraphQLRequest<IQuery>({
            request: gql`
                query UserSettings {
                    currentUser {
                        id
                        settingsCascade {
                            subjects {
                                latestSettings {
                                    id
                                    contents
                                }
                            }
                        }
                    }
                }
            `,
            variables: {},
        })

        const { currentUser } = dataOrThrowErrors(currentSettingsResponse)

        if (currentUser && currentUser.settingsCascade) {
            const emptySettings = '{}'
            const [{ latestSettings }] = currentUser.settingsCascade.subjects.slice(-1)

            if (latestSettings && latestSettings.contents !== emptySettings) {
                const updateConfigResponse = await this.makeGraphQLRequest<IMutation>({
                    request: gql`
                        mutation OverwriteSettings($subject: ID!, $lastID: Int, $contents: String!) {
                            settingsMutation(input: { subject: $subject, lastID: $lastID }) {
                                overwriteSettings(contents: $contents) {
                                    empty {
                                        alwaysNil
                                    }
                                }
                            }
                        }
                    `,
                    variables: {
                        contents: emptySettings,
                        subject: currentUser.id,
                        lastID: latestSettings.id,
                    },
                })
                dataOrThrowErrors(updateConfigResponse)
            }
        }
    }
}

function modifyJSONC(text: string, path: jsonc.JSONPath, f: (oldValue: jsonc.Node | undefined) => any): any {
    const old = jsonc.findNodeAtLocation(jsonc.parseTree(text), path)
    return jsonc.applyEdits(
        text,
        jsoncEdit.setProperty(text, path, f(old), {
            eol: '\n',
            insertSpaces: true,
            tabSize: 2,
        })
    )
}

export async function createDriverForTest(): Promise<Driver> {
    let args: string[] = []
    if (process.getuid() === 0) {
        // TODO don't run as root in CI
        console.warn('Running as root, disabling sandbox')
        args = ['--no-sandbox', '--disable-setuid-sandbox']
    }

    const launchOpt: LaunchOptions = {
        args: [...args, '--window-size=1280,1024'],
        headless: readEnvBoolean({ variable: 'HEADLESS', defaultValue: false }),
        defaultViewport: null,
    }
    const browser = await puppeteer.launch(launchOpt)
    const page = await browser.newPage()
    page.on('console', message => {
        if (message.text().includes('Download the React DevTools')) {
            return
        }
        if (message.text().includes('[HMR]') || message.text().includes('[WDS]')) {
            return
        }
        console.log(
            'Browser console message:',
            util.inspect(message, { colors: true, depth: 2, breakLength: Infinity })
        )
    })
    return new Driver(browser, page)
}
