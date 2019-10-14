import React from 'react'
import { Toast } from './Toast'
import RegexIcon from 'mdi-react/RegexIcon'
import { Link } from '../../../shared/src/components/Link'

interface State {
    visible: boolean
}

interface Props {
    isSourcegraphDotCom: boolean
}

const DISMISSED_LITERAL_SEARCH_TOAST_KEY = 'DISMISSED_LITERAL_SEARCH_TOAST'

export default class LiteralSearchToast extends React.Component<Props, State> {
    public state: State = { visible: false }

    public componentDidMount(): void {
        const canShow = localStorage.getItem(DISMISSED_LITERAL_SEARCH_TOAST_KEY) !== 'true'

        if (canShow) {
            this.showToast()
        }
    }

    public render(): JSX.Element | null {
        const docsURLPrefix = this.props.isSourcegraphDotCom ? 'https://docs.sourcegraph.com' : '/help'
        if (!this.state.visible) {
            return null
        }

        return (
            <div className="e2e-literal-search-toast">
                <Toast
                    icon={<RegexIcon size={32} />}
                    title="New regular expression toggle!"
                    subtitle="Search queries are no longer interpreted as regular expressions by default. Click the .* icon in the search bar to toggle regular expression search. Learn more about this update in the docs."
                    onDismiss={this.onDismiss}
                    cta={
                        <Link
                            to={`${docsURLPrefix}/user/search/queries`}
                            className="btn btn-primary mr-2"
                            aria-label="Visit documentation for the new regular expression toggle"
                        >
                            Learn more
                        </Link>
                    }
                />
            </div>
        )
    }

    private showToast = (): void => {
        this.setState({ visible: true })
    }

    private onDismiss = (): void => {
        localStorage.setItem(DISMISSED_LITERAL_SEARCH_TOAST_KEY, 'true')
        this.setState({ visible: false })
    }
}
