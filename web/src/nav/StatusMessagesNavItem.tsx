import { startCase } from 'lodash'
import CloudAlertIcon from 'mdi-react/CloudAlertIcon'
import CloudCheckIcon from 'mdi-react/CloudCheckIcon'
import CloudSyncIcon from 'mdi-react/CloudSyncIcon'
import React from 'react'
import { ButtonDropdown, DropdownMenu, DropdownToggle } from 'reactstrap'
import { Observable, SchedulerLike, Subscription, timer } from 'rxjs'
import { catchError, concatMap, map } from 'rxjs/operators'
import { Link } from '../../../shared/src/components/Link'
import { dataOrThrowErrors, gql } from '../../../shared/src/graphql/graphql'
import * as GQL from '../../../shared/src/graphql/schema'
import { asError, ErrorLike, isErrorLike } from '../../../shared/src/util/errors'
import { queryGraphQL } from '../backend/graphql'
import classNames from 'classnames'

export function fetchAllStatusMessages(): Observable<GQL.StatusMessage[]> {
    return queryGraphQL(
        gql`
            query StatusMessages {
                statusMessages {
                    __typename

                    ... on CloningProgress {
                        message
                    }

                    ... on SyncError {
                        message
                    }

                    ... on ExternalServiceSyncError {
                        message
                        externalService {
                            id
                            displayName
                        }
                    }
                }
            }
        `
    ).pipe(
        map(dataOrThrowErrors),
        map(data => data.statusMessages)
    )
}

type EntryType = 'warning' | 'success' | 'progress'

interface StatusMessageEntryProps {
    title: string
    text: string
    showLink?: boolean
    linkTo: string
    linkText: string
    entryType: EntryType
    linkOnClick: (event: React.MouseEvent<HTMLAnchorElement, MouseEvent>) => void
}

function entryIcon(entryType: EntryType): JSX.Element {
    switch (entryType) {
        case 'warning':
            return <CloudAlertIcon className="icon-inline mr-1" />
        case 'success':
            return <CloudCheckIcon className="icon-inline mr-1" />
        case 'progress':
            return <CloudSyncIcon className="icon-inline mr-1" />
    }
}

const StatusMessagesNavItemEntry: React.FunctionComponent<StatusMessageEntryProps> = props => (
    <div
        key={props.text}
        className={classNames(
            'status-messages-nav-item__entry mb-3',
            props.entryType && `status-messages-nav-item__entry--border-${props.entryType}`
        )}
    >
        <h4>
            {entryIcon(props.entryType)}
            {props.title}
        </h4>
        <p>{props.text}</p>
        {props.showLink && (
            <p className="status-messages-nav-item__entry-link">
                <Link to={props.linkTo} onClick={props.linkOnClick}>
                    {props.linkText}
                </Link>
            </p>
        )}
    </div>
)

interface Props {
    fetchMessages: () => Observable<GQL.StatusMessage[]>

    /** Scheduler for the refresh timer */
    scheduler?: SchedulerLike

    isSiteAdmin?: boolean
}

interface State {
    messagesOrError: GQL.StatusMessage[] | ErrorLike
    isOpen: boolean
}

const REFRESH_INTERVAL_MS = 3000

/**
 * Displays a status icon in the navbar reflecting the completion of backend
 * tasks such as repository cloning, and exposes a dropdown menu containing
 * more information on these tasks.
 */
export class StatusMessagesNavItem extends React.PureComponent<Props, State> {
    private subscriptions = new Subscription()

    public state: State = { isOpen: false, messagesOrError: [] }

    private toggleIsOpen = () => this.setState(prevState => ({ isOpen: !prevState.isOpen }))

    public componentDidMount(): void {
        this.subscriptions.add(
            timer(0, REFRESH_INTERVAL_MS, this.props.scheduler)
                .pipe(concatMap(() => this.props.fetchMessages().pipe(catchError(err => [asError(err)]))))
                .subscribe(messagesOrError => this.setState({ messagesOrError }))
        )
    }

    public componentWillUnmount(): void {
        this.subscriptions.unsubscribe()
    }

    private renderMessage(message: GQL.StatusMessage): JSX.Element | null {
        switch (message.__typename) {
            case 'CloningProgress':
                return (
                    <StatusMessagesNavItemEntry
                        key={message.message}
                        title="Repositories cloning"
                        text={message.message}
                        showLink={this.props.isSiteAdmin}
                        linkTo="/site-admin/external-services"
                        linkText="Configure external services"
                        linkOnClick={this.toggleIsOpen}
                        entryType="progress"
                    />
                )
            case 'ExternalServiceSyncError':
                return (
                    <StatusMessagesNavItemEntry
                        key={message.message}
                        title={`Syncing external service "${message.externalService.displayName}" failed:`}
                        text={message.message}
                        showLink={this.props.isSiteAdmin}
                        linkTo={`/site-admin/external-services/${message.externalService.id}`}
                        linkText={`Edit "${message.externalService.displayName}"`}
                        linkOnClick={this.toggleIsOpen}
                        entryType="warning"
                    />
                )
            case 'SyncError':
                return (
                    <StatusMessagesNavItemEntry
                        key={message.message}
                        title="Syncing repositories failed:"
                        text={message.message}
                        showLink={this.props.isSiteAdmin}
                        linkTo="/site-admin/external-services"
                        linkText="Configure external services"
                        linkOnClick={this.toggleIsOpen}
                        entryType="warning"
                    />
                )
        }
    }

    private renderIcon(): JSX.Element | null {
        if (isErrorLike(this.state.messagesOrError)) {
            return <CloudAlertIcon className="icon-inline" />
        }
        if (this.state.messagesOrError.some(({ __typename }) => __typename === 'ExternalServiceSyncError')) {
            return (
                <CloudAlertIcon
                    className="icon-inline"
                    data-tooltip={this.state.isOpen ? undefined : 'Syncing repositories failed!'}
                />
            )
        }
        if (this.state.messagesOrError.some(({ __typename }) => __typename === 'CloningProgress')) {
            return (
                <CloudSyncIcon
                    className="icon-inline"
                    data-tooltip={this.state.isOpen ? undefined : 'Cloning repositories...'}
                />
            )
        }
        return (
            <CloudCheckIcon
                className="icon-inline"
                data-tooltip={this.state.isOpen ? undefined : 'Repositories up to date'}
            />
        )
    }

    public render(): JSX.Element | null {
        return (
            <ButtonDropdown
                isOpen={this.state.isOpen}
                toggle={this.toggleIsOpen}
                className="nav-link py-0 px-0 status-messages-nav-item__nav-link"
            >
                <DropdownToggle caret={false} className="btn btn-icon" nav={true}>
                    {this.renderIcon()}
                </DropdownToggle>

                <DropdownMenu right={true} className="status-messages-nav-item__dropdown-menu">
                    <h3>External Service Status</h3>
                    {isErrorLike(this.state.messagesOrError) ? (
                        <div className="status-messages-nav-item__entry alert alert-danger mb-0">
                            <h4>Failed to load status messages:</h4>
                            <p>{startCase(this.state.messagesOrError.message)}</p>
                        </div>
                    ) : this.state.messagesOrError.length > 0 ? (
                        this.state.messagesOrError.map(m => this.renderMessage(m))
                    ) : (
                        <StatusMessagesNavItemEntry
                            title="Repositories up to date"
                            text="All repositories hosted on the configured external services are cloned."
                            showLink={this.props.isSiteAdmin}
                            linkTo="/site-admin/external-services"
                            linkText="Configure external services"
                            linkOnClick={this.toggleIsOpen}
                            entryType="success"
                        />
                    )}
                </DropdownMenu>
            </ButtonDropdown>
        )
    }
}
